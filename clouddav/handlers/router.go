package handlers

import (
	"log" // Aggiunto per il logging in AuthMiddleware e altri handler
	"net/http"
	"path/filepath" // Per unire i percorsi in modo sicuro

	"clouddav/auth"      // Assicurati che il percorso sia corretto
	"clouddav/config"    // Assicurati che il percorso sia corretto
	"clouddav/websocket" // Assicurati che il percorso sia corretto
)

// InitRoutes configura tutte le route per l'applicazione.
// Presuppone che le funzioni handler specifiche (es. handleLogin, serveIndexHTML)
// siano definite in altri file dello stesso pacchetto 'handlers'
// (es. auth_handlers.go, static_handlers.go, file_op_handlers.go).
func InitRoutes(appCfg *config.Config, wsHub *websocket.Hub, mux *http.ServeMux) {
	// Crea l'istanza del middleware di autenticazione
	// AuthMiddleware è una factory: AuthMiddleware(appCfg) restituisce func(http.Handler) http.Handler
	authMw := AuthMiddleware(appCfg) // Definita in handlers/middleware.go

	// --- Endpoint di Autenticazione ---
	// NoCacheMiddlewareFunc è un helper se l'handler è già un http.HandlerFunc
	// e si vuole che il risultato sia ancora http.HandlerFunc.
	// handleLogin e handleCallback sono http.HandlerFunc definite in auth_handlers.go
	mux.HandleFunc("/auth/login", NoCacheMiddlewareFunc(handleLogin))
	mux.HandleFunc("/auth/callback", NoCacheMiddlewareFunc(handleCallback))

	// --- Gestione File Statici e UI principale ---
	// Assumiamo che i file statici siano in una directory "static"
	// relativa alla directory di esecuzione del programma.

	// Per servire file dalla directory static/js/ sotto il percorso /js/
	jsFileServer := http.FileServer(http.Dir(filepath.Join("static", "js")))
	mux.Handle("/js/", NoCacheMiddleware(http.StripPrefix("/js/", jsFileServer)))

	// Per servire file dalla directory static/css/ sotto il percorso /css/ (se presente)
	// cssFileServer := http.FileServer(http.Dir(filepath.Join("static", "css")))
	// mux.Handle("/css/", NoCacheMiddleware(http.StripPrefix("/css/", cssFileServer)))

	// Per favicon.ico direttamente dalla root di static
	mux.Handle("/favicon.ico", NoCacheMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assicurati che il percorso sia sicuro e corretto
		http.ServeFile(w, r, filepath.Join("static", "favicon.ico"))
	})))

	// La root "/" serve index.html ed è protetta da autenticazione.
	mux.Handle("/", NoCacheMiddleware(authMw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Per una Single Page Application (SPA), spesso tutte le route non API/statiche
		// vengono reindirizzate a index.html.
		// Se il path non è esattamente "/", potresti voler servire comunque index.html
		// o restituire un 404 se preferisci una gestione più restrittiva.
		if r.URL.Path == "/" { // Servi index.html solo per la root esatta
			http.ServeFile(w, r, filepath.Join("static", "index.html"))
		} else {
			// Per altri path non gestiti esplicitamente, servi index.html (tipico per SPA)
			// In alternativa, per restituire 404: http.NotFound(w,r)
			http.ServeFile(w, r, filepath.Join("static", "index.html"))
		}
	}))))

	// --- Endpoint di Comunicazione (WebSocket e Long Polling) ---
	// Protetti da autenticazione.
	mux.Handle("/ws", NoCacheMiddleware(authMw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userClaims, _ := r.Context().Value(auth.ClaimsKey{}).(*auth.UserClaims)
		wsHub.ServeWs(w, r, userClaims)
	}))))

	mux.Handle("/lp", NoCacheMiddleware(authMw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userClaims, _ := r.Context().Value(auth.ClaimsKey{}).(*auth.UserClaims)
		wsHub.ServeLongPolling(w, r, userClaims)
	}))))

	// --- Endpoint per Operazioni su File (Download, Upload) ---
	// Protetti da autenticazione.
	// All'interno di handleDownload/handleUpload, dovrai fare i controlli di autorizzazione specifici per risorsa (authz.CheckStorageAccess).
	mux.Handle("/download", NoCacheMiddleware(authMw(http.HandlerFunc(handleDownload))))

	// handleUpload gestirà internamente le varie sotto-azioni (initiate, chunk, finalize, ecc.)
	// e interagirà con wsHub se necessario per lo stato degli upload.
	mux.Handle("/upload", NoCacheMiddleware(authMw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Passa appCfg e wsHub a handleUpload.
		// Questo presuppone che handleUpload sia definita per accettare questi parametri.
		handleUpload(w, r, appCfg, wsHub)
	}))))

	// Placeholder per altre eventuali pagine HTML statiche che potrebbero servire route dedicate
	// mux.Handle("/treeview.html", NoCacheMiddleware(authMw(http.HandlerFunc(serveTreeviewHTML))))
	// mux.Handle("/filelist.html", NoCacheMiddleware(authMw(http.HandlerFunc(serveFilelistHTML))))
}

// --- Definizioni Placeholder per gli Handler Specifici ---
// Queste funzioni dovrebbero essere implementate nei loro file dedicati
// all'interno del pacchetto handlers (es. auth_handlers.go, static_handlers.go, file_op_handlers.go).

func handleLogin(w http.ResponseWriter, r *http.Request) {
	// Implementa la logica per /auth/login
	// Esempio: loginURL, err := auth.GetLoginURL(); http.Redirect(w, r, loginURL, http.StatusFound)
	log.Println("Accesso a /auth/login")
	http.Error(w, "handleLogin non implementato", http.StatusNotImplemented)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	// Implementa la logica per /auth/callback
	// Esempio: auth.HandleCallback(r.Context(), r); http.Redirect(w, r, "/", http.StatusFound)
	log.Println("Accesso a /auth/callback")
	http.Error(w, "handleCallback non implementato", http.StatusNotImplemented)
}

// func serveIndexHTML(w http.ResponseWriter, r *http.Request) { // Già gestito inline sopra
// 	http.ServeFile(w, r, filepath.Join("static", "index.html"))
// }

func serveFavicon(w http.ResponseWriter, r *http.Request) {
	// La logica è già inline in mux.Handle("/favicon.ico", ...)
	// Questa funzione separata potrebbe non essere necessaria se la lasci inline.
	// Se la usi, assicurati che il path sia corretto.
	http.ServeFile(w, r, filepath.Join("static", "favicon.ico"))
}

func handleDownload(w http.ResponseWriter, r *http.Request /* appCfg *config.Config */) {
	// Implementa la logica per /download
	// Dovrai recuperare userClaims dal contesto r.Context().Value(auth.ClaimsKey{}).(*auth.UserClaims)
	// e appCfg (se non lo passi come argomento, potresti averlo in una struct handler).
	log.Println("Accesso a /download")
	http.Error(w, "handleDownload non implementato", http.StatusNotImplemented)
}

func handleUpload(w http.ResponseWriter, r *http.Request, appCfg *config.Config, hub *websocket.Hub) {
	// Implementa la logica per /upload (multi-azione)
	// Dovrai recuperare userClaims dal contesto r.Context().Value(auth.ClaimsKey{}).(*auth.UserClaims)
	log.Println("Accesso a /upload")
	http.Error(w, "handleUpload non implementato", http.StatusNotImplemented)
}

// func serveTreeviewHTML(w http.ResponseWriter, r *http.Request) {
// 	http.ServeFile(w, r, filepath.Join("static", "treeview.html"))
// }
// func serveFilelistHTML(w http.ResponseWriter, r *http.Request) {
// 	http.ServeFile(w, r, filepath.Join("static", "filelist.html"))
// }
