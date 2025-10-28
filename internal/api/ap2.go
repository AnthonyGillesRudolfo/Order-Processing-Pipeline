package api

import (
    "net/http"

    internalap2 "github.com/AnthonyGillesRudolfo/Order-Processing-Pipeline/internal/ap2"
    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// RegisterAP2Routes mounts AP2 HTTP endpoints to the mux.
func RegisterAP2Routes(mux *http.ServeMux) {
    mux.Handle("/ap2/mandates", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleCreateMandate), "ap2-create-mandate"))
    mux.Handle("/ap2/mandates/", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleGetMandate), "ap2-get-mandate"))
    mux.Handle("/ap2/intents", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleCreateIntent), "ap2-create-intent"))
    mux.Handle("/ap2/authorize", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleAuthorize), "ap2-authorize"))
    mux.Handle("/ap2/execute", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleExecute), "ap2-execute"))
    mux.Handle("/ap2/status/", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleGetStatus), "ap2-get-status"))
    mux.Handle("/ap2/refunds", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleRefund), "ap2-refund"))
    mux.Handle("/ap2/refunds/", otelhttp.NewHandler(http.HandlerFunc(internalap2.HandleGetRefund), "ap2-get-refund"))
}

