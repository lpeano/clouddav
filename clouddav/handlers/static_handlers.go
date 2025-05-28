package handlers

import (
	"net/http"

	"clouddav/config" // Assicurati che questo import sia corretto
)

// ServeIndexHTML serve il file index.html principale.
// Ora è chiamato da AddAPIRoutes e include il middleware di autenticazione.
func ServeIndexHTML(appConfig *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/index.html")
	}
}

// ServeTreeviewHTML serve il file treeview.html.
func ServeTreeviewHTML(appConfig *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/treeview.html")
	}
}

// ServeFilelistHTML serve il file filelist.html.
func ServeFilelistHTML(appConfig *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/filelist.html")
	}
}

// ServeFavicon serve il file favicon.ico.
func ServeFavicon(appConfig *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/favicon.ico")
	}
}

// Nota: Gli handler per /js/ e /css/ sono gestiti direttamente in AddStaticRoutes
// usando http.StripPrefix e http.FileServer.
