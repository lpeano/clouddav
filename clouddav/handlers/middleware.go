package handlers

import (
	"log"
	"net/http"

	// Assicurati di importare i pacchetti necessari per l'autenticazione,
	// ad esempio "clouddav/auth" e "clouddav/config"
	// "clouddav/auth"
	"clouddav/auth"
	"clouddav/config"
)

// NoCacheMiddleware aggiunge header per prevenire la memorizzazione nella cache.
// Accetta http.Handler e restituisce http.Handler.
func NoCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
	})
}

// AuthMiddleware gestisce l'autenticazione.
// Questa è una factory che restituisce il middleware effettivo.
// Accetta http.Handler e restituisce http.Handler.
func AuthMiddleware(appCfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Se l'autenticazione non è abilitata, procedi direttamente.
			if !appCfg.EnableAuth {
				next.ServeHTTP(w, r)
				return
			}

			// Esempio di logica di autenticazione (da adattare):
			// Qui dovresti estrarre i claims dell'utente, ad esempio da un cookie o da un contesto.
			// Per questo esempio, assumiamo che i claims siano già nel contesto della richiesta
			// (potrebbero essere stati inseriti da un altro middleware o gestore precedente se usi sessioni).
			// Se non ci sono claims o l'utente non è autorizzato, reindirizza al login.

			userClaims, ok := r.Context().Value(auth.ClaimsKey{}).(*auth.UserClaims) // Usa la tua chiave corretta
			if !ok || userClaims == nil {
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("AuthMiddleware: User claims not found in context for path %s, redirecting to login.", r.URL.Path)
				}
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}
			
			// Verifica se l'utente è autorizzato a livello di applicazione
			// (basato su appCfg.AzureAD.AllowedGroups)
			if !auth.IsUserAuthorized(userClaims, appCfg) {
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("AuthMiddleware: User %s not authorized for the application (not in allowed groups).", userClaims.Email)
				}
				http.Error(w, "Accesso negato: Utente non autorizzato per l'applicazione.", http.StatusForbidden)
				return
			}

			// Se l'autenticazione ha successo, procedi con l'handler successivo.
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("AuthMiddleware: User %s authorized, proceeding with handler for %s.", userClaims.Email, r.URL.Path)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// NoCacheMiddlewareFunc è un helper se vuoi applicare NoCache a un http.HandlerFunc direttamente
// e vuoi che il risultato sia ancora un http.HandlerFunc (meno comune per la composizione generale).
// Per coerenza, è meglio usare NoCacheMiddleware che accetta http.Handler.
func NoCacheMiddlewareFunc(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
	}
}
