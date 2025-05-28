package handlers

import (
	"context"
	"log"
	"net/http"

	"bytes"
	"clouddav/auth"
	"clouddav/config"
	"clouddav/websocket" // Assicurati che questo import sia corretto
	"io"
)

// HandleWebSocket gestisce le richieste di connessione WebSocket.
func HandleWebSocket(hub *websocket.Hub, appConfig *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, _ := getClaimsFromContext(r.Context()) // Recupera i claims dal contesto (impostati da AuthMiddleware)
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleWebSocket: New WebSocket connection attempt. User claims present: %t", claims != nil)
			if claims != nil {
				log.Printf("[DEBUG] HandleWebSocket: User email: %s", claims.Email)
				log.Printf("[DEBUG] HandleWebSocket: User groups (IDs): %v", claims.Groups)
				log.Printf("[DEBUG] HandleWebSocket: User groups (Names): %v", claims.GroupNames)
			}
		}
		hub.ServeWs(w, r, claims)
	}
}

// HandleLongPolling gestisce le richieste di Long Polling come fallback.
func HandleLongPolling(hub *websocket.Hub, appConfig *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, _ := getClaimsFromContext(r.Context()) // Recupera i claims dal contesto
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleLongPolling: New Long Polling request. User claims present: %t", claims != nil)
			if claims != nil {
				log.Printf("[DEBUG] HandleLongPolling: User email: %s", claims.Email)
				log.Printf("[DEBUG] HandleLongPolling: User groups (IDs): %v", claims.Groups)
				log.Printf("[DEBUG] HandleLongPolling: User groups (Names): %v", claims.GroupNames)
			}
		}

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Received Long Polling request: %s %s", r.Method, r.URL.Path)
		}
		// Log del corpo della richiesta per debug, se necessario
		if config.IsLogLevel(config.LogLevelDebug) {
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				log.Printf("Error reading Long Polling request body: %v", err)
				// Ripristina il corpo per l'elaborazione successiva anche se c'è un errore di lettura per il logging
				r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			} else {
				log.Printf("Long Polling request body: %s", string(bodyBytes))
				// Ripristina il corpo per l'elaborazione successiva
				r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			}
		}

		hub.ServeLongPolling(w, r, claims)
	}
}

// getClaimsFromContext è una funzione helper per estrarre i claims dal contesto.
// Questa funzione era implicita in AuthMiddleware e ora è resa esplicita per l'uso negli handler.
func getClaimsFromContext(ctxHttp context.Context) (*auth.UserClaims, bool) {
	claims, ok := ctxHttp.Value(auth.ClaimsKey{}).(*auth.UserClaims)
	return claims, ok
}
