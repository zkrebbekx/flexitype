package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// advisoryLockKey serializes concurrent migration runners over the schema they
// are migrating, on the ONE path that can still hold a lock safely: the
// single-transaction fallback, where pg_advisory_xact_lock is released by the
// transaction that took it.
//
// The multi-transaction path must not use a session lock. flexitype supports
// transaction-mode pooling, where consecutive statements run on different
// backends: a session lock taken there does not serialize the runner's own
// later transactions, and it is released onto a connection another client then
// borrows — stranding it on an idle backend where every subsequent migration
// blocks on it forever, with no way for the application to clear it. That path
// uses a lease row instead (see migrateLease).
//
// The key is derived from current_schema() rather than being a constant,
// because the lock protects one schema's migrations. Two flexitype schemas in
// one database — separate deployments, or the per-package schemas the test
// suites use — otherwise serialize against each other for no reason, and can
// deadlock through the objects a migration shares with them (pg_trgm lives in
// public and is created by migration 000004).
const advisoryLockKey = `hashtext(current_schema() || ':flexitype-migrate')`

// Migrate applies embedded up-migrations in order, tracking progress in
// flexitype_schema_migrations, then runs any pending data backfills. It is safe
// to call on every startup and from embedded deployments: concurrent runners
// serialize on an advisory lock. Runtime is forward-only — down-migrations
// exist for local development and reversibility testing via MigrateDown, and
// are never applied at startup.
//
// Each migration is applied in its own transaction, not all of them in one.
// The all-or-nothing form cannot coexist with the statements a large
// deployment needs: CREATE INDEX CONCURRENTLY is rejected inside a transaction
// block, and a data backfill held inside the schema transaction keeps its DDL
// locks for the whole scan — which is how a whole-table backfill came to block
// every value write in the fleet for the duration of the upgrade.
//
// A migration file may carry directives in its header:
//
//	-- +flexitype:no-transaction
//
// which runs its statements one at a time outside any transaction. Statements
// in such a file must be idempotent, because a failure part-way leaves the
// earlier ones applied and the file unrecorded.
//
// See docs/upgrades.md for the rolling-upgrade contract and the rules for
// writing a migration that is safe against a live fleet.
func Migrate(ctx context.Context, tx db.Transactor) error {
	conn, ok := tx.(db.SessionConnector)
	if !ok {
		// A transactor that cannot pin a connection cannot hold a session
		// advisory lock or run a statement outside a transaction. Fall back to
		// the single-transaction form, which is correct for every migration
		// that carries no directive.
		return migrateInOneTransaction(ctx, tx)
	}
	return conn.WithConn(ctx, func(q db.QueryExecer) error {
		if err := ensureMigrationTables(ctx, q); err != nil {
			return err
		}
		// A lease row rather than a session lock: the state lives in a table,
		// so it survives the connection moving between backends under a
		// transaction-mode pooler, and it expires rather than stranding when
		// a runner dies.
		lease, err := acquireMigrationLease(ctx, q)
		if err != nil {
			return err
		}
		// The mid-statement heartbeat renews on the POOL, because a running
		// statement occupies the pinned connection. The concrete pool-level
		// transactor is itself a QueryExecer; a transactor that is not gets
		// no heartbeat and keeps the between-statement renewals only.
		if pool, ok := tx.(db.QueryExecer); ok {
			lease.pool = pool
		}
		defer lease.release(ctx)

		if err := applyPending(ctx, q, lease); err != nil {
			return err
		}
		return runBackfills(ctx, q)
	})
}

// migrateInOneTransaction is the fallback for a transactor with no pinned
// connection. It applies every pending migration in one transaction and
// refuses a migration that declares it must run outside one, rather than
// applying it in the form its author ruled out.
func migrateInOneTransaction(ctx context.Context, tx db.Transactor) error {
	return tx.InTransaction(ctx, func(tx db.Transactor) error {
		q := txExecer(tx)
		if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(`+advisoryLockKey+`)`); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}
		if err := ensureMigrationTables(ctx, q); err != nil {
			return err
		}
		pending, err := pendingMigrations(ctx, q)
		if err != nil {
			return err
		}
		for _, m := range pending {
			if m.directives.NoTransaction {
				return fmt.Errorf("migration %s declares no-transaction but this transactor cannot pin a connection", m.name)
			}
			if _, err := q.ExecContext(ctx, m.body); err != nil {
				return fmt.Errorf("apply migration %s: %w", m.name, err)
			}
			if err := recordMigration(ctx, q, m.version); err != nil {
				return err
			}
		}
		return runBackfills(ctx, q)
	})
}

// ensureMigrationTables creates the runner bookkeeping tables.
//
// Each CREATE runs through createTableIfNotExists, because CREATE TABLE IF NOT
// EXISTS is NOT safe against a concurrent identical CREATE: both sessions pass
// the existence check and the loser fails on a unique violation in a catalogue
// index. Six replicas starting together — the rolling-deploy shape — hit that
// before any lock could be taken, since this is the step that creates the
// table the lock lives in.
func ensureMigrationTables(ctx context.Context, q db.QueryExecer) error {
	if err := createTableIfNotExists(ctx, q, "migrations table",
		`CREATE TABLE IF NOT EXISTS flexitype_schema_migrations (
		   version    INTEGER PRIMARY KEY,
		   applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		 )`); err != nil {
		return err
	}
	if err := createTableIfNotExists(ctx, q, "migration lock table",
		`CREATE TABLE IF NOT EXISTS flexitype_schema_lock (
		   id          INTEGER PRIMARY KEY CHECK (id = 1),
		   holder      TEXT NOT NULL,
		   acquired_at TIMESTAMPTZ NOT NULL,
		   expires_at  TIMESTAMPTZ NOT NULL
		 )`); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO flexitype_schema_lock (id, holder, acquired_at, expires_at)
		 VALUES (1, '', now(), now()) ON CONFLICT (id) DO NOTHING`); err != nil {
		return fmt.Errorf("ensure migration lock row: %w", err)
	}
	// Backfills are tracked separately from schema versions: a backfill is
	// resumable and may span many runs, so "the schema is at version N" and
	// "the data behind version N has caught up" are different facts.
	return createTableIfNotExists(ctx, q, "backfill table",
		`CREATE TABLE IF NOT EXISTS flexitype_schema_backfill (
		   name         TEXT PRIMARY KEY,
		   completed_at TIMESTAMPTZ NOT NULL DEFAULT now()
		 )`)
}

// createTableIfNotExists runs a CREATE TABLE IF NOT EXISTS and treats a
// concurrent creation as success.
//
// Postgres does not make IF NOT EXISTS atomic against another session running
// the same statement: both see the table absent, both proceed, and the loser
// fails with 23505 on a catalogue index (pg_type_typname_nsp_index) or 42P07
// "relation already exists". Either way the table now exists, which is all the
// caller asked for.
func createTableIfNotExists(ctx context.Context, q db.QueryExecer, what, stmt string) error {
	_, err := q.ExecContext(ctx, stmt)
	if err == nil || isConcurrentCreate(err) {
		return nil
	}
	return fmt.Errorf("ensure %s: %w", what, err)
}

// isConcurrentCreate reports whether an error is another session having
// created the same object first.
func isConcurrentCreate(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	switch pqErr.Code {
	case "23505": // unique_violation, on a catalogue index
		return true
	case "42P07": // duplicate_table
		return true
	case "42710": // duplicate_object, e.g. a constraint
		return true
	}
	return false
}

// Migration-lease timings. The lease is refreshed between statements, so the
// TTL only has to outlast one statement; leaseTTL is generous because a
// CREATE INDEX CONCURRENTLY over a large table is one statement.
// leaseWait MUST exceed leaseTTL. A runner that dies holding the lease leaves
// a row with a live expires_at that nobody renews, and the only way another
// runner recovers is by outlasting it. With a shorter wait, every replica
// booting inside that window gave up before the lease could expire — inside a
// container startup path, so the orchestrator restarted it and it repeated,
// and a whole generation could not boot.
const (
	leaseTTL      = 15 * time.Minute
	leaseWait     = leaseTTL + 2*time.Minute
	leasePoll     = 250 * time.Millisecond
	leaseRenewGap = time.Minute
	// leaseHeartbeat is how often the mid-statement heartbeat renews the
	// lease while a no-transaction statement runs (see execWithHeartbeat).
	// INVARIANT: leaseHeartbeat must stay comfortably below leaseTTL. At one
	// minute against the fifteen-minute TTL, the heartbeat gets about
	// fourteen renewal attempts before the lease can expire, so a transient
	// renewal failure retries on the next tick instead of killing a long
	// index build. TestMigrationLeaseHeartbeatIntegration pins the margin.
	leaseHeartbeat = time.Minute
	// leaseReleaseTimeout bounds the write that frees the lease. It runs on a
	// context detached from the caller's, because releasing on a cancelled
	// startup context was a no-op and stranded the lease for the full TTL
	// even though the process was alive.
	leaseReleaseTimeout = 10 * time.Second
)

// migrateLease is a mutual exclusion held in a TABLE ROW rather than in
// session state.
//
// A session-scoped advisory lock cannot be used here. flexitype supports
// transaction-mode pooling, and under it the runner's statements run on
// whichever backend the pooler hands out — so the lock neither serializes the
// runner's own later transactions nor stays with it. Measured through
// PgBouncer with a contended pool, five of six concurrent Migrate() calls
// failed on raw DDL collisions and one lock was left held on an idle pooled
// backend, where it blocked every subsequent migration attempt with no
// diagnostic and no application-level recovery.
//
// A row does not have those properties: it is visible to every backend, and it
// carries an expiry, so a runner that dies frees it without an operator having
// to find and terminate a connection by hand.
type migrateLease struct {
	q      db.QueryExecer // the pinned migration connection
	holder string
	now    func() time.Time

	// pool is a second, POOLED executor for the mid-statement heartbeat. The
	// pinned connection is occupied while a statement runs, so a renewal
	// issued during one must travel on another connection. nil disables the
	// heartbeat and keeps only the between-statement renewals.
	pool db.QueryExecer

	// heartbeatEvery overrides leaseHeartbeat in tests; zero means the
	// default.
	heartbeatEvery time.Duration

	mu        sync.Mutex // guards renewedAt against the heartbeat goroutine
	renewedAt time.Time
}

// markRenewed records a successful acquisition or renewal.
func (l *migrateLease) markRenewed() {
	l.mu.Lock()
	l.renewedAt = l.now()
	l.mu.Unlock()
}

// sinceRenewed reports how long ago the last renewal succeeded.
func (l *migrateLease) sinceRenewed() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.now().Sub(l.renewedAt)
}

// acquireMigrationLease takes the lease, waiting for a live holder to finish
// or expire.
func acquireMigrationLease(ctx context.Context, q db.QueryExecer) (*migrateLease, error) {
	l := &migrateLease{q: q, holder: leaseHolderID(), now: time.Now}
	deadline := l.now().Add(leaseWait)
	for {
		ok, err := l.tryAcquire(ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			return l, nil
		}
		if l.now().After(deadline) {
			holder, expires := l.currentHolder(ctx)
			return nil, fmt.Errorf(
				"acquire migration lease: %q has held it for more than %s (expires_at %s); "+
					"if that runner is gone, the lease frees itself at that time",
				holder, leaseWait, expires)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(leasePoll):
		}
	}
}

// tryAcquire claims the lease if it is free or expired. The read and the write
// are one transaction over the singleton row, so two runners cannot both win.
func (l *migrateLease) tryAcquire(ctx context.Context) (bool, error) {
	var taken bool
	if err := l.q.GetContext(ctx, &taken,
		`WITH claim AS (
		   UPDATE flexitype_schema_lock
		      SET holder = $1, acquired_at = now(), expires_at = now() + $2::interval
		    WHERE id = 1 AND (holder = '' OR expires_at <= now())
		    RETURNING 1
		 )
		 SELECT EXISTS (SELECT 1 FROM claim)`,
		l.holder, leaseTTL.String()); err != nil {
		return false, fmt.Errorf("acquire migration lease: %w", err)
	}
	if taken {
		l.markRenewed()
	}
	return taken, nil
}

// renew extends the lease if it is close to expiring. It is called between
// statements, so a long migration does not lose the lease it holds.
//
// A renewal that finds the row held by someone else is an error: the previous
// statement outlasted the TTL and another runner took over, so continuing
// would apply DDL alongside them.
func (l *migrateLease) renew(ctx context.Context) error {
	if l.sinceRenewed() < leaseRenewGap {
		return nil
	}
	held, err := l.renewOn(ctx, l.q)
	if err != nil {
		return fmt.Errorf("renew migration lease: %w", err)
	}
	if !held {
		return fmt.Errorf("lost the migration lease: a statement outlasted the %s lease and another runner took over", leaseTTL)
	}
	return nil
}

// renewOn runs one renewal on the given executor and reports whether this
// runner still holds the lease. It carries no rate limit of its own: renew
// applies the between-statement gap, and the heartbeat's ticker is its own
// rate limit.
func (l *migrateLease) renewOn(ctx context.Context, q db.QueryExecer) (bool, error) {
	var held bool
	if err := q.GetContext(ctx, &held,
		`WITH renew AS (
		   UPDATE flexitype_schema_lock
		      SET expires_at = now() + $2::interval
		    WHERE id = 1 AND holder = $1
		    RETURNING 1
		 )
		 SELECT EXISTS (SELECT 1 FROM renew)`,
		l.holder, leaseTTL.String()); err != nil {
		return false, err
	}
	if held {
		l.markRenewed()
	}
	return held, nil
}

// execWithHeartbeat runs one no-transaction statement on the pinned
// connection while a goroutine renews the lease on the pooled executor.
//
// The pinned connection is busy for the whole statement, so a renewal cannot
// travel on it — which is why renew alone was not enough. A statement longer
// than leaseTTL — a CREATE INDEX CONCURRENTLY over a large value table — let
// the lease expire mid-build. The next replica then took the lease, replayed
// the file, and its invalid-index reap issued a DROP INDEX CONCURRENTLY
// against the in-flight build; the two runners undid each other's work for
// the length of the deploy.
//
// The heartbeat separates two failures:
//   - Another runner HOLDS the lease: the heartbeat cancels the statement's
//     context at once, because finishing would apply DDL alongside the new
//     holder.
//   - The renewal QUERY fails: the lease row is untouched and stays valid
//     until expires_at, so the heartbeat retries on later ticks. It gives up
//     only after a full leaseTTL passes with no successful renewal, because
//     at that point the lease may genuinely have expired.
func (l *migrateLease) execWithHeartbeat(ctx context.Context, q db.QueryExecer, stmt string) error {
	if l.pool == nil {
		// No second executor is available. Keep the between-statement
		// renewals, which is the pre-heartbeat behaviour.
		_, err := q.ExecContext(ctx, stmt)
		return err
	}

	stmtCtx, cancelStmt := context.WithCancel(ctx)
	defer cancelStmt()
	stop := make(chan struct{})
	beat := make(chan error, 1)
	go func() { beat <- l.heartbeat(ctx, stop, cancelStmt) }()

	_, execErr := q.ExecContext(stmtCtx, stmt)
	close(stop)
	if hbErr := <-beat; hbErr != nil {
		// The heartbeat cancelled the statement. Name the cause rather than
		// returning the driver's bare cancellation error.
		return hbErr
	}
	return execErr
}

// heartbeat renews the lease on the pooled executor until stop closes. It
// returns nil on a clean stop. On a lost lease, or after a full leaseTTL with
// no successful renewal, it cancels the running statement and returns the
// reason.
func (l *migrateLease) heartbeat(ctx context.Context, stop <-chan struct{}, cancelStmt context.CancelFunc) error {
	every := l.heartbeatEvery
	if every <= 0 {
		every = leaseHeartbeat
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-ctx.Done():
			// The caller's context ended. The statement dies with it and
			// reports that itself.
			return nil
		case <-tick.C:
		}
		held, err := l.renewOn(ctx, l.pool)
		switch {
		case err != nil && l.sinceRenewed() < leaseTTL:
			// Transient: the lease row still holds until expires_at.
			continue
		case err != nil:
			cancelStmt()
			return fmt.Errorf("cancelled the running migration statement: no lease renewal succeeded for %s, so the lease may have expired: %w", leaseTTL, err)
		case !held:
			cancelStmt()
			return fmt.Errorf("lost the migration lease mid-statement: another runner holds it, so the statement was cancelled rather than finishing alongside it")
		}
	}
}

// release frees the lease. A failure here is not fatal: the lease expires.
//
// It runs on a DETACHED context. The commonest reason Migrate returns is that
// the caller's context ended, and releasing on that same context was a no-op
// — so an embedded deployment with a cancellable startup context stranded the
// lease for the full TTL while the process was alive and able to free it.
func (l *migrateLease) release(ctx context.Context) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaseReleaseTimeout)
	defer cancel()
	_, _ = l.q.ExecContext(rctx,
		`UPDATE flexitype_schema_lock SET holder = '', expires_at = now()
		  WHERE id = 1 AND holder = $1`, l.holder)
}

// currentHolder reads who holds the lease and when it expires, for the error
// a waiting runner reports. An operator needs the blocker named: without it
// the failure said only that someone else had the lease.
func (l *migrateLease) currentHolder(ctx context.Context) (holder, expires string) {
	var row struct {
		Holder    string    `db:"holder"`
		ExpiresAt time.Time `db:"expires_at"`
	}
	if err := l.q.GetContext(ctx, &row,
		`SELECT holder, expires_at FROM flexitype_schema_lock WHERE id = 1`); err != nil {
		return "unknown", "unknown"
	}
	return row.Holder, row.ExpiresAt.UTC().Format(time.RFC3339)
}

// leaseHolderID names the runner in the lock row, so an operator inspecting a
// held lease can see who has it.
func leaseHolderID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return host + "/" + ulid.New().String()
}

// migration is one embedded up-migration, parsed.
type migration struct {
	name       string
	version    int
	body       string
	directives migrationDirectives
}

// pendingMigrations returns the unapplied up-migrations in version order.
func pendingMigrations(ctx context.Context, q db.QueryExecer) ([]migration, error) {
	var versions []int
	if err := q.SelectContext(ctx, &versions, `SELECT version FROM flexitype_schema_migrations`); err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	applied := make(map[int]bool, len(versions))
	for _, v := range versions {
		applied[v] = true
	}

	names, err := upMigrations()
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return nil, err
		}
		if applied[version] {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		directives, err := parseDirectives(name, string(body))
		if err != nil {
			return nil, err
		}
		out = append(out, migration{name: name, version: version, body: string(body), directives: directives})
	}
	return out, nil
}

// applyPending applies each pending migration on the pinned connection, in its
// own transaction unless the file declares otherwise.
func applyPending(ctx context.Context, q db.QueryExecer, lease *migrateLease) error {
	pending, err := pendingMigrations(ctx, q)
	if err != nil {
		return err
	}
	for _, m := range pending {
		if err := lease.renew(ctx); err != nil {
			return err
		}
		if m.directives.NoTransaction {
			if err := applyOutsideTransaction(ctx, q, m, lease); err != nil {
				return err
			}
			continue
		}
		if err := applyInTransaction(ctx, q, m); err != nil {
			return err
		}
	}
	return nil
}

// applyInTransaction claims a migration and applies it in one transaction.
//
// The CLAIM COMES FIRST, and that ordering is the point. Inserting the version
// row before the DDL makes the row itself the mutual exclusion: a second
// runner's insert blocks on the primary key until this transaction ends, then
// finds the version taken and skips. The lock is therefore held for exactly
// the transaction that needs it, by the database, with no session state — so
// it is correct through a transaction-mode pooler, and correct even if the
// lease has been lost. Both the claim and the DDL roll back together on
// failure, so a failed migration leaves nothing recorded.
func applyInTransaction(ctx context.Context, q db.QueryExecer, m migration) (err error) {
	if _, err := q.ExecContext(ctx, `BEGIN`); err != nil {
		return fmt.Errorf("begin migration %s: %w", m.name, err)
	}
	defer func() {
		if err != nil {
			_, _ = q.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	var claimed bool
	if err = q.GetContext(ctx, &claimed,
		`WITH claim AS (
		   INSERT INTO flexitype_schema_migrations (version) VALUES ($1)
		   ON CONFLICT (version) DO NOTHING
		   RETURNING 1
		 )
		 SELECT EXISTS (SELECT 1 FROM claim)`, m.version); err != nil {
		return fmt.Errorf("claim migration %s: %w", m.name, err)
	}
	if !claimed {
		// Another runner applied it while this one was reading the pending
		// list. Roll back and move on; err stays nil, so the deferred
		// ROLLBACK does not fire.
		if _, cerr := q.ExecContext(ctx, `ROLLBACK`); cerr != nil {
			return fmt.Errorf("roll back already-applied migration %s: %w", m.name, cerr)
		}
		return nil
	}
	if _, err = q.ExecContext(ctx, m.body); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.name, err)
	}
	if _, err = q.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.name, err)
	}
	return nil
}

// applyOutsideTransaction runs a no-transaction migration one statement at a
// time, then records it. The record is not atomic with the statements — that
// is the price of running outside a transaction — so every statement in such a
// file must be idempotent, and the runner replays the whole file if it is
// interrupted.
func applyOutsideTransaction(ctx context.Context, q db.QueryExecer, m migration, lease *migrateLease) error {
	// Reap invalid indexes this file builds, BEFORE replaying it.
	//
	// A failed CREATE INDEX CONCURRENTLY leaves an INVALID index behind. The
	// name then exists, so `IF NOT EXISTS` skips the rebuild for ever, and a
	// file that also drops the index it replaces — migration 000028 — removed
	// the working index and left only the invalid one. Every read that index
	// served then sequentially scanned the table, permanently, with the
	// schema version reading as current and no error at any point. The
	// trigger is an ordinary pod lifecycle event during a deploy: an
	// OOMKill, an eviction or a lock timeout part-way through the build.
	//
	// Dropping an invalid index is safe precisely because it is invalid: no
	// query can use it, and the CREATE that follows rebuilds it.
	if err := reapInvalidIndexes(ctx, q, m); err != nil {
		return err
	}
	for _, stmt := range splitStatements(m.body) {
		if statementIsEmpty(stmt) {
			continue
		}
		// A statement here can run for a long time — CREATE INDEX
		// CONCURRENTLY over a large table is the reason this path exists — so
		// the lease is refreshed between statements, and a heartbeat renews
		// it DURING each statement on a second connection (the statement
		// occupies this one). A build longer than the lease TTL would
		// otherwise lose the lease mid-flight, and the runner that took over
		// would reap the half-built index out from under it.
		if err := lease.renew(ctx); err != nil {
			return err
		}
		if err := lease.execWithHeartbeat(ctx, q, stmt); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
	}
	return recordMigration(ctx, q, m.version)
}

// concurrentIndexNames matches the index name in a CREATE INDEX CONCURRENTLY
// statement, with or without IF NOT EXISTS and with or without UNIQUE.
var concurrentIndexNames = regexp.MustCompile(
	`(?is)CREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z_][a-zA-Z0-9_$]*)`)

// reapInvalidIndexes drops any INVALID index whose name this migration
// creates concurrently, so the replay rebuilds it rather than skipping it.
//
// It covers every no-transaction migration rather than the one that exposed
// the hazard (000018, 000021 and 000025 build indexes concurrently too), and
// it is a no-op in the ordinary case: the catalogue query matches nothing.
func reapInvalidIndexes(ctx context.Context, q db.QueryExecer, m migration) error {
	for _, match := range concurrentIndexNames.FindAllStringSubmatch(m.body, -1) {
		name := match[1]
		var invalid bool
		if err := q.GetContext(ctx, &invalid,
			`SELECT EXISTS (
			   SELECT 1 FROM pg_index i
			     JOIN pg_class c ON c.oid = i.indexrelid
			     JOIN pg_namespace n ON n.oid = c.relnamespace
			    WHERE c.relname = $1
			      AND n.nspname = current_schema()
			      AND NOT i.indisvalid)`, name); err != nil {
			return fmt.Errorf("check index %s for migration %s: %w", name, m.name, err)
		}
		if !invalid {
			continue
		}
		// Quoted with %q semantics via pq.QuoteIdentifier: the name comes
		// from the embedded migration files, but the query is built by
		// concatenation, so it is quoted rather than trusted.
		if _, err := q.ExecContext(ctx,
			`DROP INDEX CONCURRENTLY IF EXISTS `+pq.QuoteIdentifier(name)); err != nil {
			return fmt.Errorf("reap invalid index %s for migration %s: %w", name, m.name, err)
		}
	}
	return nil
}

// recordMigration marks a version applied.
func recordMigration(ctx context.Context, q db.QueryExecer, version int) error {
	if _, err := q.ExecContext(ctx,
		`INSERT INTO flexitype_schema_migrations (version) VALUES ($1)
		 ON CONFLICT (version) DO NOTHING`, version); err != nil {
		return fmt.Errorf("record migration %06d: %w", version, err)
	}
	return nil
}

// KnownSchemaVersion is the highest migration version this binary carries.
// Compare it against the database to detect a mixed-version fleet.
func KnownSchemaVersion() (int, error) {
	names, err := upMigrations()
	if err != nil {
		return 0, err
	}
	highest := 0
	for _, name := range names {
		v, err := migrationVersion(name)
		if err != nil {
			return 0, err
		}
		if v > highest {
			highest = v
		}
	}
	return highest, nil
}

// UnknownSchemaVersions returns the applied versions the binary does not
// carry — the schema is newer than this build. It is how a rolling deploy
// makes a mixed-version fleet visible: the previous generation keeps serving
// against a schema the next generation migrated, which flexitype supports
// (each release's migrations stay compatible with the previous binary), but an
// operator should still be able to see that it is happening.
//
// It returns nothing when the migrations table does not exist yet.
func UnknownSchemaVersions(ctx context.Context, q db.QueryExecer) ([]int, error) {
	// to_regclass resolves through search_path, so this asks whether the table
	// exists in the schema this connection actually writes to — not whether
	// some other schema in the database happens to have one.
	var exists bool
	if err := q.GetContext(ctx, &exists,
		`SELECT to_regclass('flexitype_schema_migrations') IS NOT NULL`); err != nil {
		return nil, fmt.Errorf("probe migrations table: %w", err)
	}
	if !exists {
		return nil, nil
	}
	known, err := KnownSchemaVersion()
	if err != nil {
		return nil, err
	}
	var newer []int
	if err := q.SelectContext(ctx, &newer,
		`SELECT version FROM flexitype_schema_migrations WHERE version > $1 ORDER BY version`, known); err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	return newer, nil
}

// MigrateDown reverts applied migrations whose version is greater than
// target, newest first, running each .down.sql and removing its
// schema-migrations row. It is NOT called at startup; use it in local
// development and reversibility tests. target=0 reverts everything.
//
// Like Migrate, each migration is reverted in its own transaction unless its
// down-file declares -- +flexitype:no-transaction, which a file dropping an
// index CONCURRENTLY must.
//
// Rollback of a deployed release is redeploying the previous binary, not
// running this: see docs/upgrades.md.
func MigrateDown(ctx context.Context, tx db.Transactor, target int) error {
	conn, ok := tx.(db.SessionConnector)
	if !ok {
		return fmt.Errorf("migrate down: this transactor cannot pin a connection")
	}
	return conn.WithConn(ctx, func(q db.QueryExecer) error {
		if err := ensureMigrationTables(ctx, q); err != nil {
			return err
		}
		lease, err := acquireMigrationLease(ctx, q)
		if err != nil {
			return err
		}
		defer lease.release(ctx)

		var versions []int
		if err := q.SelectContext(ctx, &versions,
			`SELECT version FROM flexitype_schema_migrations WHERE version > $1 ORDER BY version DESC`,
			target); err != nil {
			return fmt.Errorf("read applied migrations: %w", err)
		}

		for _, version := range versions {
			down, err := downMigration(version)
			if err != nil {
				return err
			}
			body, err := migrationsFS.ReadFile("migrations/" + down)
			if err != nil {
				return fmt.Errorf("read down migration %s: %w", down, err)
			}
			directives, err := parseDirectives(down, string(body))
			if err != nil {
				return err
			}
			if err := revert(ctx, q, down, version, string(body), directives); err != nil {
				return err
			}
		}
		// A reverted schema must re-run its backfills if it is re-applied.
		if _, err := q.ExecContext(ctx, `DELETE FROM flexitype_schema_backfill`); err != nil {
			return fmt.Errorf("clear backfill records: %w", err)
		}
		return nil
	})
}

// revert runs one down-migration and forgets its version.
func revert(ctx context.Context, q db.QueryExecer, name string, version int, body string, d migrationDirectives) (err error) {
	forget := func() error {
		if _, err := q.ExecContext(ctx,
			`DELETE FROM flexitype_schema_migrations WHERE version = $1`, version); err != nil {
			return fmt.Errorf("record reverting %06d: %w", version, err)
		}
		return nil
	}

	if d.NoTransaction {
		for _, stmt := range splitStatements(body) {
			if statementIsEmpty(stmt) {
				continue
			}
			if _, err := q.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("revert migration %s: %w", name, err)
			}
		}
		return forget()
	}

	if _, err := q.ExecContext(ctx, `BEGIN`); err != nil {
		return fmt.Errorf("begin reverting %s: %w", name, err)
	}
	defer func() {
		if err != nil {
			_, _ = q.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	if _, err = q.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("revert migration %s: %w", name, err)
	}
	if err = forget(); err != nil {
		return err
	}
	if _, err = q.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit reverting %s: %w", name, err)
	}
	return nil
}

// upMigrations lists embedded up-migrations in version order.
func upMigrations() ([]string, error) {
	return listMigrations(".up.sql")
}

// downMigration finds the .down.sql for one version.
func downMigration(version int) (string, error) {
	downs, err := listMigrations(".down.sql")
	if err != nil {
		return "", err
	}
	for _, name := range downs {
		if v, err := migrationVersion(name); err == nil && v == version {
			return name, nil
		}
	}
	return "", fmt.Errorf("no down migration for version %d", version)
}

func listMigrations(suffix string) ([]string, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// migrationVersion parses the numeric prefix of "000001_init.up.sql".
func migrationVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("malformed migration name %q", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("malformed migration version in %q: %w", name, err)
	}
	return version, nil
}
