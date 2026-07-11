package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// stubAuth resolves one fixed token to one fixed account.
type stubAuth struct {
	token   string
	account serviceaccount.Account
}

func (s stubAuth) Authenticate(token string) (serviceaccount.Account, error) {
	if token == s.token {
		return s.account, nil
	}
	return serviceaccount.Account{}, errUnknown
}

var errUnknown = &authErr{}

type authErr struct{}

func (*authErr) Error() string { return "unknown" }

// echo terminates the chain, reporting the actor/tenant/scopes the
// middleware stamped and whether the admin gate would pass.
func echoContext(w http.ResponseWriter, r *http.Request) {
	actor := uow.ActorFromContext(r.Context())
	tenant := uow.TenantFromContext(r.Context())
	admin := hasScope(r.Context(), serviceaccount.ScopeAdmin)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(string(actor.Kind) + "|" + actor.Name + "|" + tenant.String() + "|" + boolStr(admin)))
}

func boolStr(b bool) string {
	if b {
		return "admin"
	}
	return "noadmin"
}

func serve(auth serviceaccount.Authenticator, method, token string) *httptest.ResponseRecorder {
	log := logger.New(logger.Config{})
	h := authenticate(auth, log)(http.HandlerFunc(echoContext))
	req := httptest.NewRequest(method, "/api/v1/thing", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthenticateMiddleware(t *testing.T) {
	writer := serviceaccount.Account{
		ID: "acct-writer", Name: "writer", TenantID: "acme",
		Scopes: []serviceaccount.Scope{serviceaccount.ScopeRead, serviceaccount.ScopeWrite},
	}
	auth := stubAuth{token: "good", account: writer}

	Convey("Given the auth middleware over a stub authenticator", t, func() {
		Convey("When no bearer token is supplied", func() {
			rec := serve(auth, http.MethodGet, "")
			Convey("Then the request is rejected 401", func() {
				So(rec.Code, ShouldEqual, http.StatusUnauthorized)
			})
		})

		Convey("When an unknown token is supplied", func() {
			rec := serve(auth, http.MethodGet, "bad")
			Convey("Then the request is rejected 401", func() {
				So(rec.Code, ShouldEqual, http.StatusUnauthorized)
			})
		})

		Convey("When a valid token GETs (read scope suffices)", func() {
			rec := serve(auth, http.MethodGet, "good")
			Convey("Then it passes and stamps the account's tenant and identity", func() {
				So(rec.Code, ShouldEqual, http.StatusOK)
				So(rec.Body.String(), ShouldEqual, "service_account|writer|acme|noadmin")
			})
		})

		Convey("When a valid token POSTs (write scope required)", func() {
			rec := serve(auth, http.MethodPost, "good")
			Convey("Then the write scope permits it", func() {
				So(rec.Code, ShouldEqual, http.StatusOK)
			})
		})

		Convey("When a read-only account POSTs", func() {
			readOnly := serviceaccount.Account{
				ID: "acct-reader", Name: "reader", TenantID: "acme",
				Scopes: []serviceaccount.Scope{serviceaccount.ScopeRead},
			}
			rec := serve(stubAuth{token: "good", account: readOnly}, http.MethodPost, "good")
			Convey("Then it is forbidden 403", func() {
				So(rec.Code, ShouldEqual, http.StatusForbidden)
			})
		})

		Convey("When an admin account is used", func() {
			adminAcct := serviceaccount.Account{
				ID: "acct-admin", Name: "root", TenantID: "acme",
				Scopes: []serviceaccount.Scope{serviceaccount.ScopeAdmin},
			}
			rec := serve(stubAuth{token: "good", account: adminAcct}, http.MethodPost, "good")
			Convey("Then admin implies write and the admin gate passes", func() {
				So(rec.Code, ShouldEqual, http.StatusOK)
				So(rec.Body.String(), ShouldEndWith, "|admin")
			})
		})

		Convey("When authentication is disabled (nil authenticator, dev mode)", func() {
			rec := serve(nil, http.MethodPost, "")
			Convey("Then the request runs as the system actor with admin scope", func() {
				So(rec.Code, ShouldEqual, http.StatusOK)
				So(rec.Body.String(), ShouldEqual, "system|dev|default|admin")
			})
		})
	})
}
