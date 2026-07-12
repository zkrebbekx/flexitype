//go:build js && wasm

// The browser playground: the full flexitype service — usecases, REST API,
// activity log, FQL, search index — compiled to WebAssembly over the
// in-memory store. The admin console talks to it through a fetch shim that
// routes /api/v1 requests into this handler instead of the network. Data
// lives in the browser tab and resets on reload.
package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"syscall/js"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/internal/demo"
	httpapi "github.com/zkrebbekx/flexitype/internal/interfaces/http"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
)

func main() {
	svc := flexitype.NewInMemory(flexitype.WithSearchIndex())

	ctx := context.Background()
	if err := demo.Seed(ctx, svc.Interactors(ctx)); err != nil {
		js.Global().Get("console").Call("error", "flexitype: demo seed failed: "+err.Error())
	}

	handler := httpapi.NewHandler(httpapi.ServerConfig{
		Factory: svc.Factory(),
		Logger:  logger.New(logger.Config{Level: "warn"}),
		Health:  health.NewService("flexitype", "playground"),
		Reindex: svc.ReindexSearch,
		GraphQL: svc.GraphQLEngine(),
	})

	js.Global().Set("__flexitypeFetch", js.FuncOf(func(_ js.Value, args []js.Value) any {
		method := args[0].String()
		path := args[1].String()
		body := ""
		if len(args) > 2 && !args[2].IsUndefined() && !args[2].IsNull() {
			body = args[2].String()
		}

		return js.Global().Get("Promise").New(js.FuncOf(func(_ js.Value, pargs []js.Value) any {
			resolve := pargs[0]
			// A goroutine keeps the JS event loop free: handlers may yield
			// (timers, channels) and blocking the callback would deadlock.
			go func() {
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				if body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				resolve.Invoke(js.ValueOf(map[string]any{
					"status":      rec.Code,
					"body":        rec.Body.String(),
					"contentType": rec.Header().Get("Content-Type"),
				}))
			}()
			return nil
		}))
	}))

	js.Global().Set("__flexitypeReady", js.ValueOf(true))
	if cb := js.Global().Get("__flexitypeOnReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}

	// Keep the Go runtime alive for the page's lifetime.
	select {}
}
