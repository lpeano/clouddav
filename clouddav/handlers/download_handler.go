package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"

	// Assicurati che questo import sia corretto
	"clouddav/config"
	"clouddav/internal/authz" // Per CheckStorageAccess
	"clouddav/storage"        // Per StorageProvider e errori
	"errors"
)

// HandleDownload gestisce le richieste di download dei file.
func HandleDownload(appConfig *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, _ := getClaimsFromContext(r.Context())
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleDownload: Download request. User claims present: %t", claims != nil)
			if claims != nil {
				log.Printf("[DEBUG] HandleDownload: User email: %s", claims.Email)
				// Non loggare i gruppi qui per brevità, già fatto in AuthMiddleware
			}
		}

		storageName := r.URL.Query().Get("storage")
		itemPath := r.URL.Query().Get("path")

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleDownload: Request for storage '%s', path '%s'", storageName, itemPath)
		}

		if storageName == "" || itemPath == "" {
			http.Error(w, "Parameters 'storage' and 'path' required", http.StatusBadRequest)
			return
		}

		// Autorizzazione: Verifica se l'utente ha il permesso di leggere.
		if err := authz.CheckStorageAccess(r.Context(), claims, storageName, itemPath, "read", appConfig); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				http.Error(w, "Access denied: read permission required", http.StatusForbidden)
			} else {
				log.Printf("Error checking storage access for download '%s/%s': %v", storageName, itemPath, err)
				http.Error(w, "Internal server error during access check", http.StatusInternalServerError)
			}
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] HandleDownload: Storage access granted.")
		}

		provider, ok := storage.GetProvider(storageName)
		if !ok {
			http.Error(w, "Storage provider not found", http.StatusNotFound)
			return
		}

		reader, err := provider.OpenReader(r.Context(), claims, itemPath)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "Item not found", http.StatusNotFound)
			} else if errors.Is(err, storage.ErrPermissionDenied) { // Anche se già controllato, il provider potrebbe avere logica interna
				http.Error(w, "Access denied: read permission required by provider", http.StatusForbidden)
			} else {
				log.Printf("Error opening item '%s/%s': %v", storageName, itemPath, err)
				http.Error(w, "Error downloading item", http.StatusInternalServerError)
			}
			return
		}
		defer reader.Close()

		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(itemPath)))
		w.Header().Set("Content-Type", "application/octet-stream")

		_, err = io.Copy(w, reader)
		if err != nil {
			log.Printf("Error copying item stream for download '%s/%s': %v", storageName, itemPath, err)
			// Non inviare http.Error qui se lo stream è già iniziato, potrebbe corrompere la risposta.
		}
	}
}
