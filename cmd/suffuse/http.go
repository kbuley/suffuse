package main

import (
	"net"
	"net/http"

	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
)

// serveHTTPGateway runs an HTTP/1.1 server on ln serving the grpc-gateway mux.
func serveHTTPGateway(ln net.Listener, mux *gwruntime.ServeMux) error {
	srv := &http.Server{Handler: mux}
	return srv.Serve(ln)
}
