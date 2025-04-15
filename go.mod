module github.com/zac300/flexitype

go 1.23.0

toolchain go1.23.8

require (
	github.com/jmoiron/sqlx v1.4.0
	github.com/lib/pq v1.10.9
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/bufbuild/connect-go v1.10.0
	github.com/pressly/goose/v3 v3.24.2
	golang.org/x/net v0.39.0
	google.golang.org/protobuf v1.36.6
)

require (
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

// Replace directives for local modules
replace github.com/zac300/flexitype/api/flexitypev1 => ./api/flexitypev1

replace github.com/zac300/flexitype/api/flexitypev1connect => ./api/flexitypev1connect
