module github.com/zkrebbekx/flexitype/client

go 1.25.0

// v1.6.0 carried Client.ForTenant, which sent the X-Flexitype-Tenant header
// for the `read_any_tenant` scope. That scope is withdrawn — see the root
// module — so the method is gone in v1.7.0. Nothing else in v1.6.0 is
// affected; v1.7.0 carries all of it.
retract v1.6.0
