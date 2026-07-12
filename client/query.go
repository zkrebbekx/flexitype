package client

import (
	"context"
	"iter"
	"net/http"
	"net/url"
)

// QueryOptions carry the FQL scope and pagination for a query.
type QueryOptions struct {
	ListOptions
	// Locale and Channel restrict scoped-attribute predicates to one scope;
	// zero matches base (unscoped) values.
	Locale  string
	Channel string
}

func (o QueryOptions) values(typ, fql string) url.Values {
	q := url.Values{}
	q.Set("type", typ)
	q.Set("q", fql)
	if o.Locale != "" {
		q.Set("locale", o.Locale)
	}
	if o.Channel != "" {
		q.Set("channel", o.Channel)
	}
	return q
}

// QueryPage runs one page of an FQL query over the given root type.
func (c *Client) QueryPage(ctx context.Context, typ, fql string, opts ...QueryOptions) (*Page[EntitySummary], error) {
	o := QueryOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return listPage[EntitySummary](ctx, c, "/query", o.values(typ, fql), o.ListOptions)
}

// Query runs an FQL query and iterates every matching entity across pages:
//
//	for row, err := range c.Query(ctx, "product", `price > 100`) {
//	    if err != nil { return err }
//	    ...
//	}
func (c *Client) Query(ctx context.Context, typ, fql string, opts ...QueryOptions) iter.Seq2[EntitySummary, error] {
	o := QueryOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return paginate(func(cursor string) (*Page[EntitySummary], error) {
		o.Cursor = cursor
		return c.QueryPage(ctx, typ, fql, o)
	})
}

// ValidateQuery parses and binds an FQL query without running it, returning a
// *APIError (code VALIDATION, with a "position" detail) when it is invalid.
func (c *Client) ValidateQuery(ctx context.Context, typ, fql string) error {
	return c.do(ctx, http.MethodPost, "/query/validate", nil, map[string]string{"type": typ, "q": fql}, nil)
}
