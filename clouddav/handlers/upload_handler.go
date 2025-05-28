package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"clouddav/config"
	"clouddav/internal/authz"
	"clouddav/storage"
	"clouddav/storage/azureblob" // Importa specifici provider se necessario per type assertion
	"clouddav/storage/local"     // Importa specifici provider se necessario per type assertion
	"clouddav/websocket"
)

const MAX_MEMORY_UPLOAD = 400 << 20 // 400 MB

// HandleUpload gestisce le varie fasi dell'upload di file.
func HandleUpload(appConfig *config.Config, hub *websocket.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, _ := getClaimsFromContext(r.Context())
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleUpload: Upload request. User claims present: %t", claims != nil)
			if claims != nil {
				log.Printf("[DEBUG] HandleUpload: User email: %s", claims.Email)
			}
		}

		contentType := r.Header.Get("Content-Type")
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Received upload request with Content-Type: %s", contentType)
		}

		var err error
		if strings.HasPrefix(contentType, "multipart/form-data") {
			err = r.ParseMultipartForm(MAX_MEMORY_UPLOAD)
		} else if contentType == "application/x-www-form-urlencoded" {
			err = r.ParseForm()
		} else {
			log.Printf("Unsupported Content-Type for upload: %s", contentType)
			http.Error(w, "Unsupported Content-Type for upload", http.StatusBadRequest)
			return
		}

		if err != nil {
			log.Printf("Error parsing form for upload: %v", err)
			http.Error(w, "Error parsing form for upload", http.StatusBadRequest)
			return
		}

		storageName := r.FormValue("storage")
		itemPath := r.FormValue("path")
		action := r.FormValue("action")

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleUpload: Action '%s' for storage '%s', path '%s'", action, storageName, itemPath)
		}

		if storageName == "" || itemPath == "" || action == "" {
			log.Printf("Missing required parameters for upload: storage='%s', path='%s', action='%s'", storageName, itemPath, action)
			http.Error(w, "Parameters 'storage', 'path', and 'action' are required", http.StatusBadRequest)
			return
		}

		// Autorizzazione: Verifica se l'utente ha il permesso di scrivere.
		// Per 'status', potrebbe essere sufficiente 'read', ma per coerenza manteniamo 'write'
		// dato che è un'operazione legata all'upload.
		requiredAccess := "write"
		if action == "status" { // Potremmo rilassare questo a "read" se lo stato non è sensibile
			// requiredAccess = "read" // Per ora, manteniamo write per semplicità
		}

		if err := authz.CheckStorageAccess(r.Context(), claims, storageName, itemPath, requiredAccess, appConfig); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				http.Error(w, fmt.Sprintf("Access denied: %s permission required", requiredAccess), http.StatusForbidden)
			} else {
				log.Printf("Error checking storage access for upload '%s/%s', action '%s': %v", storageName, itemPath, action, err)
				http.Error(w, "Internal server error during access check", http.StatusInternalServerError)
			}
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleUpload: Storage access granted for %s operation.", requiredAccess)
		}

		provider, ok := storage.GetProvider(storageName)
		if !ok {
			http.Error(w, "Storage provider not found", http.StatusNotFound)
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleUpload: Provider %T (val: %v)", provider, provider) // Logga tipo e valore del provider
		}

		uploadKey := fmt.Sprintf("%s:%s", storageName, itemPath)
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleUpload: uploadKey %s", uploadKey)
		}
		
		var currentUserEmail string
		if claims != nil {
			currentUserEmail = claims.Email
		} else {
			currentUserEmail = "unknown_user"
		}


		switch action {
		case "initiate":
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("[DEBUG] HandleUpload: initiate action")
			}
			
			// Controllo preliminare per upload concorrenti
			hub.FileUPloadsMutex.Lock()
			if sessionState, exists := hub.OngoingFileUploads[uploadKey]; exists {
				hub.FileUPloadsMutex.Unlock() // Rilascia il lock se c'è un conflitto immediato
				log.Printf("Upload conflict: File '%s' is already being uploaded by '%s'. Current user: '%s'", uploadKey, sessionState.Claims.Email, currentUserEmail)
				http.Error(w, fmt.Sprintf("File '%s' è già in fase di caricamento da parte di %s.", itemPath, sessionState.Claims.Email), http.StatusConflict)
				return
			}
			hub.FileUPloadsMutex.Unlock() 
			log.Printf("Initial lock released for initiate action of %s", uploadKey)


			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Handling upload initiate for storage '%s', path '%s' by user '%s'", storageName, itemPath, currentUserEmail)
			}

			totalFileSizeStr := r.FormValue("total_file_size")
			chunkSizeStr := r.FormValue("chunk_size")

			totalFileSize, parseErr1 := strconv.ParseInt(totalFileSizeStr, 10, 64)
			chunkSize, parseErr2 := strconv.ParseInt(chunkSizeStr, 10, 64)
			
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("\t totalFileSize: %d\n\tchunkSize %d", totalFileSize, chunkSize)
			}


			if parseErr1 != nil || parseErr2 != nil || totalFileSize < 0 || chunkSize <= 0 { // totalFileSize può essere 0 per file vuoti
				http.Error(w, "Missing or invalid total_file_size or chunk_size for initiate action", http.StatusBadRequest)
				return
			}

			var uploadedSize int64
			var errInitiate error // Rinominato per chiarezza

			// La chiamata al provider.InitiateUpload può essere lunga, non deve tenere bloccato il mutex.
			switch p := provider.(type) {
			case *local.LocalFilesystemProvider:
				uploadedSize, errInitiate = p.InitiateUpload(r.Context(), claims, itemPath, totalFileSize, chunkSize)
			case *azureblob.AzureBlobStorageProvider:
				uploadedSize, errInitiate = p.InitiateUpload(r.Context(), claims, itemPath, totalFileSize, chunkSize)
			default:
				errInitiate = storage.ErrNotImplemented
			}

			if errInitiate != nil {
				log.Printf("Error initiating upload for '%s/%s': %v", storageName, itemPath, errInitiate)
				if errors.Is(errInitiate, storage.ErrPermissionDenied) {
					http.Error(w, "Access denied: write permission required", http.StatusForbidden)
				} else if errors.Is(errInitiate, storage.ErrNotFound) {
					http.Error(w, "Destination not found", http.StatusNotFound)
				} else if errors.Is(errInitiate, storage.ErrNotImplemented) {
					http.Error(w, "Upload not supported for this storage type", http.StatusNotImplemented)
				} else {
					http.Error(w, fmt.Sprintf("Error initiating upload: %v", errInitiate), http.StatusInternalServerError)
				}
				return
			}
			
			// Ora, blocca il mutex SOLO per aggiungere la sessione alla mappa.
			log.Printf("Setting Mutex for final add of %s", uploadKey)
			hub.FileUPloadsMutex.Lock()
			log.Printf("Mutex locked for final add of %s", uploadKey)

			// È buona pratica ricontrollare l'esistenza qui per gestire una possibile race condition
			if _, currentExists := hub.OngoingFileUploads[uploadKey]; currentExists {
				hub.FileUPloadsMutex.Unlock()
				log.Printf("Upload conflict (race condition before final add): File '%s' became active.", uploadKey)
				http.Error(w, "File è diventato attivo durante l'inizializzazione, riprovare.", http.StatusConflict)
				// Considerare la pulizia delle risorse temporanee del provider qui se necessario
				return
			}

			hub.OngoingFileUploads[uploadKey] = &websocket.UploadSessionState{
				Claims:         claims,
				StorageName:    storageName,
				ItemPath:       itemPath,
				LastActivity:   time.Now(),
				ProviderType:   provider.Type(),
			}
			hub.FileUPloadsMutex.Unlock()
			log.Printf("Store Setted. Mutex unlocked for %s", uploadKey)


			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]int64{"uploaded_size": uploadedSize})

		case "chunk":
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("[DEBUG] HandleUpload: chunk action")
			}
			if !strings.HasPrefix(contentType, "multipart/form-data") {
				log.Printf("Received chunk action with incorrect Content-Type: %s", contentType)
				http.Error(w, "Chunk action requires multipart/form-data Content-Type", http.StatusBadRequest)
				return
			}

			if config.IsLogLevel(config.LogLevelDebug) { // Modificato da Info a Debug
				log.Printf("Handling upload chunk for storage '%s', path '%s'", storageName, itemPath)
			}
			file, _, err := r.FormFile("chunk")
			if err != nil {
				log.Printf("Error getting file chunk for '%s/%s': %v", storageName, itemPath, err)
				http.Error(w, fmt.Sprintf("Error getting file chunk: %v", err), http.StatusBadRequest)
				return
			}
			defer file.Close()

			blockID := r.FormValue("block_id")
			chunkIndexStr := r.FormValue("chunk_index")
			chunkSizeStr := r.FormValue("chunk_size") // Recupera chunk_size

			chunkIndex, parseErr1 := strconv.ParseInt(chunkIndexStr, 10, 64)
			chunkSizeVal, parseErr2 := strconv.ParseInt(chunkSizeStr, 10, 64) // Parsifica chunk_size

			if parseErr1 != nil || parseErr2 != nil || chunkSizeVal <= 0 { // Controlla chunkSizeVal
				http.Error(w, "Missing or invalid chunk_index or chunk_size for chunk action", http.StatusBadRequest)
				return
			}

			var writeErr error
			switch p := provider.(type) {
			case *local.LocalFilesystemProvider:
				chunkData, readErr := io.ReadAll(file)
				if readErr != nil {
					log.Printf("Error reading file chunk for local upload '%s/%s': %v", storageName, itemPath, readErr)
					http.Error(w, fmt.Sprintf("Error reading file chunk: %v", readErr), http.StatusInternalServerError)
					return
				}
				writeErr = p.WriteChunk(r.Context(), claims, itemPath, chunkData, chunkIndex, chunkSizeVal) // Passa chunkSizeVal
			case *azureblob.AzureBlobStorageProvider:
				if blockID == "" {
					http.Error(w, "Parameter 'block_id' is required for azure-blob chunk upload", http.StatusBadRequest)
					return
				}
				writeErr = p.WriteChunk(r.Context(), claims, itemPath, blockID, file, chunkIndex)
			default:
				writeErr = storage.ErrNotImplemented
			}

			if writeErr != nil {
				log.Printf("Error writing chunk for '%s/%s': %v", storageName, itemPath, writeErr)
				if errors.Is(writeErr, storage.ErrPermissionDenied) {
					http.Error(w, "Access denied: write permission required", http.StatusForbidden)
				} else if errors.Is(writeErr, storage.ErrNotImplemented) {
					http.Error(w, "Chunk upload not supported for this storage type", http.StatusNotImplemented)
				} else {
					http.Error(w, fmt.Sprintf("Error writing chunk: %v", writeErr), http.StatusInternalServerError)
				}
				return
			}

			hub.FileUPloadsMutex.Lock()
			if sessionState, exists := hub.OngoingFileUploads[uploadKey]; exists {
				sessionState.LastActivity = time.Now()
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Updated last activity for upload '%s' to %s", uploadKey, sessionState.LastActivity.Format(time.RFC3339))
				}
			}
			hub.FileUPloadsMutex.Unlock()

			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Successfully wrote chunk %d for storage '%s', path '%s'", chunkIndex, storageName, itemPath)
			}
			w.WriteHeader(http.StatusOK)

		case "finalize":
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Handling upload finalize for storage '%s', path '%s'", storageName, itemPath)
			}
			var errFinalize error // Rinominato per chiarezza
			var blockIDs []string
			clientSHA256 := r.FormValue("client_sha256")
			totalFileSizeStr := r.FormValue("total_file_size")

			totalFileSize, parseErr := strconv.ParseInt(totalFileSizeStr, 10, 64)
			if parseErr != nil || totalFileSize < 0 { // totalFileSize può essere 0 per file vuoti
				http.Error(w, "Missing or invalid total_file_size for finalize action", http.StatusBadRequest)
				return
			}

			switch p := provider.(type) {
			case *local.LocalFilesystemProvider:
				errFinalize = p.FinalizeUpload(claims, itemPath, clientSHA256) // totalFileSize non è più necessario qui per il provider locale
			case *azureblob.AzureBlobStorageProvider:
				blockIDsJSON := r.FormValue("block_ids")
				if blockIDsJSON == "" {
					http.Error(w, "Parameter 'block_ids' is required for azure-blob finalize", http.StatusBadRequest)
					return
				}
				if jsonErr := json.Unmarshal([]byte(blockIDsJSON), &blockIDs); jsonErr != nil {
					http.Error(w, "Invalid 'block_ids' format", http.StatusBadRequest)
					return
				}
				errFinalize = p.FinalizeUpload(r.Context(), claims, itemPath, blockIDs, clientSHA256)
			default:
				errFinalize = storage.ErrNotImplemented
			}

			hub.FileUPloadsMutex.Lock()
			delete(hub.OngoingFileUploads, uploadKey)
			hub.FileUPloadsMutex.Unlock()

			if errFinalize != nil {
				log.Printf("Error finalizing upload for '%s/%s': %v", storageName, itemPath, errFinalize)
				if errors.Is(errFinalize, storage.ErrPermissionDenied) {
					http.Error(w, "Access denied: write permission required", http.StatusForbidden)
				} else if errors.Is(errFinalize, storage.ErrNotImplemented) {
					http.Error(w, "Upload finalization not supported for this storage type", http.StatusNotImplemented)
				} else if errors.Is(errFinalize, storage.ErrIntegrityCheckFailed) {
					http.Error(w, "File integrity check failed after upload. Hashes do not match.", http.StatusInternalServerError)
				} else {
					http.Error(w, fmt.Sprintf("Error finalizing upload: %v", errFinalize), http.StatusInternalServerError)
				}
				return
			}
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Successfully finalized upload for storage '%s', path '%s'", storageName, itemPath)
			}
			w.WriteHeader(http.StatusOK)

		case "cancel":
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Handling upload cancel for storage '%s', path '%s'", storageName, itemPath)
			}
			var errCancel error // Rinominato per chiarezza

			switch p := provider.(type) {
			case *local.LocalFilesystemProvider:
				errCancel = p.CancelUpload(claims, itemPath)
			case *azureblob.AzureBlobStorageProvider:
				errCancel = p.CancelUpload(r.Context(), claims, itemPath)
			default:
				errCancel = nil
			}

			hub.FileUPloadsMutex.Lock()
			delete(hub.OngoingFileUploads, uploadKey)
			hub.FileUPloadsMutex.Unlock()

			if errCancel != nil {
				log.Printf("Error cancelling upload for '%s/%s': %v", storageName, itemPath, errCancel)
				if errors.Is(errCancel, storage.ErrPermissionDenied) {
					http.Error(w, "Access denied: write permission required", http.StatusForbidden)
				} else if !strings.Contains(errCancel.Error(), "no ongoing upload session found") && !errors.Is(errCancel, storage.ErrNotFound) {
					http.Error(w, fmt.Sprintf("Error cancelling upload: %v", errCancel), http.StatusInternalServerError)
					return
				}
			}
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Successfully handled upload cancel for storage '%s', path '%s'", storageName, itemPath)
			}
			w.WriteHeader(http.StatusOK)

		case "status":
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Handling upload status for storage '%s', path '%s'", storageName, itemPath)
			}
			var uploadedSize int64
			var errStatus error // Rinominato per chiarezza

			switch p := provider.(type) {
			case *local.LocalFilesystemProvider:
				uploadedSize, errStatus = p.GetUploadedSize(claims, itemPath)
			case *azureblob.AzureBlobStorageProvider:
				uploadedSize, errStatus = p.GetUploadedSize(r.Context(), claims, itemPath)
			default:
				uploadedSize = 0
				errStatus = nil
			}

			if errStatus != nil {
				log.Printf("Error getting upload status for '%s/%s': %v", storageName, itemPath, errStatus)
				if errors.Is(errStatus, storage.ErrPermissionDenied) {
					http.Error(w, "Access denied: read permission required", http.StatusForbidden)
				} else {
					http.Error(w, fmt.Sprintf("Error getting upload status: %v", errStatus), http.StatusInternalServerError)
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]int64{"uploaded_size": uploadedSize})

		default:
			log.Printf("Received invalid upload action: %s for storage '%s', path '%s'", action, storageName, itemPath)
			http.Error(w, "Invalid upload action", http.StatusBadRequest)
		}
	}
}
