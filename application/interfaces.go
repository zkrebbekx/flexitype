// Package application wires flexitype's usecases behind a request-scoped
// factory. The factory builds fresh dataloader-backed repositories per
// request and a unit of work carrying the standard pre-commit (activity
// log), post-commit (event dispatch) and rollback commit handlers.
package application

import (
	"context"

	"github.com/zkrebbekx/flexitype/application/activity"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Repositories is one request-scoped set of domain repositories. A fresh
// set means fresh dataloader caches, so nothing leaks across requests or
// tenants.
type Repositories struct {
	TypeDefinitions typedefRepository
	Attributes      attributeRepository
	Values          valueRepository
	Dependencies    dependencyRepository
}

// Narrow aliases keep the struct readable without re-importing domain
// packages at every call site.
type (
	typedefRepository    = domaintypedef.Repository
	attributeRepository  = domainattribute.Repository
	valueRepository      = domainvalue.Repository
	dependencyRepository = domaindependency.Repository
)

// Factory creates request-scoped interactor sets.
type Factory interface {
	New(ctx context.Context) *Interactors
}

// Interactors groups every usecase for one request.
type Interactors struct {
	typeDefs *apptypedef.Interactor
	attrs    *appattribute.Interactor
	values   *appvalue.Interactor
	deps     *appdependency.Interactor
	activity *ActivityInteractor
}

// TypeDefinitions returns the type-definition usecases.
func (i *Interactors) TypeDefinitions() *apptypedef.Interactor { return i.typeDefs }

// Attributes returns the attribute-definition usecases.
func (i *Interactors) Attributes() *appattribute.Interactor { return i.attrs }

// Values returns the attribute-value usecases.
func (i *Interactors) Values() *appvalue.Interactor { return i.values }

// Dependencies returns the dependency usecases.
func (i *Interactors) Dependencies() *appdependency.Interactor { return i.deps }

// Activity returns the audit-log read usecases.
func (i *Interactors) Activity() *ActivityInteractor { return i.activity }

// ActivityInteractor serves the audit-log read side.
type ActivityInteractor struct {
	log activity.Log
}

// ActivityListInput holds filter and pagination arguments.
type ActivityListInput struct {
	Entity   string
	EntityID string
	Actor    string
	Page     db.PageArgs
}

// ActivityListOutput is one page of audit entries.
type ActivityListOutput struct {
	Items    []activity.Entry
	PageInfo db.PageInfo
}

// List returns a filtered, paginated slice of the activity log.
func (a *ActivityInteractor) List(ctx context.Context, in ActivityListInput) (*ActivityListOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, err
	}
	items, total, err := a.log.List(ctx, activity.Filter{
		TenantID: uow.TenantFromContext(ctx),
		Entity:   in.Entity,
		EntityID: in.EntityID,
		Actor:    in.Actor,
	}, page)
	if err != nil {
		return nil, err
	}
	return &ActivityListOutput{
		Items:    items,
		PageInfo: db.BuildPageInfo(page, len(items), total),
	}, nil
}
