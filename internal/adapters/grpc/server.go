package grpc

import (
	"context"
	"fmt"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"net/http"

	"github.com/zac300/flexitype/api/flexitypev1connect"
	"github.com/zac300/flexitype/internal/application/services"
)

// ConnectServer implements the FlexiType gRPC service using Connect
type ConnectServer struct {
	typeService     *services.TypeService
	instanceService *services.InstanceService
	httpServer      *http.Server
}

// NewConnectServer creates a new FlexiType Connect gRPC server
func NewConnectServer(typeService *services.TypeService, instanceService *services.InstanceService) *ConnectServer {
	return &ConnectServer{
		typeService:     typeService,
		instanceService: instanceService,
	}
}

// Start starts the Connect gRPC server on the specified port
func (s *ConnectServer) Start(port int) error {
	// Create a new FlexiType service
	service := &flexiTypeServiceServer{
		typeService:     s.typeService,
		instanceService: s.instanceService,
	}

	// Create API path handlers for the service
	mux := http.NewServeMux()
	path, handler := flexitypev1connect.NewFlexiTypeServiceHandler(service)
	mux.Handle(path, handler)

	// Configure the HTTP server
	addr := fmt.Sprintf(":%d", port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	fmt.Printf("FlexiType Connect gRPC server listening on %s\n", addr)
	return s.httpServer.ListenAndServe()
}

// Stop stops the gRPC server
func (s *ConnectServer) Stop() error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(context.Background())
	}
	return nil
}
