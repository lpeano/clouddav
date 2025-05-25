package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"clouddav/auth"
	"clouddav/config"
	"clouddav/internal/authz"
	"clouddav/storage"
	"clouddav/storage/azureblob"
	"clouddav/storage/local"
	websocket "clouddav/websocket"
)

// ClaimsKey is the key to store user claims in the request context.
type ClaimsKey struct{}

var wsHub *websocket.Hub
var appConfig *config.Config

// InitHandlers initializes HTTP handlers and the WebSocket Hub.
// Ora accetta un *http.ServeMux per registrare gli handler.
func InitHandlers(cfg *config.Config, hub *websocket.Hub, mux *http.ServeMux) {
	appConfig = cfg
	wsHub = hub

	// Registra gli handler dinamici e statici sul mux fornito.
	// Applica il middleware NoCacheMiddleware e AuthMiddleware dove necessario.

	// Handler per l'autenticazione
	mux.HandleFunc("/auth/login", NoCacheMiddleware(handleLogin))
	mux.HandleFunc("/auth/callback", NoCacheMiddleware(handleCallback))

	// Handler per le API e le pagine principali (richiedono autenticazione)
	// Nota: serveStaticFile per "/" è gestito qui per la pagina principale.
	mux.Handle("/", NoCacheMiddleware(AuthMiddleware(http.HandlerFunc(serveIndexHTML)).(http.HandlerFunc))) // Serve index.html per la root
	mux.Handle("/ws", NoCacheMiddleware(AuthMiddleware(http.HandlerFunc(handleWebSocket)).(http.HandlerFunc)))
	mux.Handle("/lp", NoCacheMiddleware(AuthMiddleware(http.HandlerFunc(handleLongPolling)).(http.HandlerFunc)))
	mux.Handle("/download", NoCacheMiddleware(AuthMiddleware(http.HandlerFunc(handleDownload)).(http.HandlerFunc)))
	mux.Handle("/upload", NoCacheMiddleware(AuthMiddleware(http.HandlerFunc(handleUpload)).(http.HandlerFunc)))

	// Handler per il favicon.ico
	mux.HandleFunc("/favicon.ico", NoCacheMiddleware(http.HandlerFunc(serveFavicon)))

	// Handler per le directory di file statici (CSS, JS, immagini, ecc.)
	// http.FileServer serve i file relativi alla directory specificata.
	// http.StripPrefix rimuove il prefisso dal percorso della richiesta prima di passarlo a FileServer.
	// Ad esempio, una richiesta a "/js/ui.js" diventerà "/ui.js" per http.FileServer,
	// che cercherà "static/js/ui.js".
	// Questi handler devono essere registrati con Handle, non HandleFunc, perché http.StripPrefix
	// restituisce un http.Handler, non un http.HandlerFunc.
	mux.Handle("/js/", NoCacheMiddleware(http.StripPrefix("/js/", http.FileServer(http.Dir("static/js"))).(http.HandlerFunc)))
	mux.Handle("/css/", NoCacheMiddleware(http.StripPrefix("/css/", http.FileServer(http.Dir("static/css"))).(http.HandlerFunc)))
}

// NoCacheMiddleware è un middleware che aggiunge intestazioni per disabilitare la cache.
func NoCacheMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
	}
}

// serveIndexHTML serve il file index.html.
func serveIndexHTML(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/index.html")
}

// serveFavicon serve il file favicon.ico.
func serveFavicon(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/favicon.ico")
}


// serveStaticFile e serveStaticDir sono stati rimossi o rifattorizzati.
// La logica per servire i file statici è ora gestita direttamente dagli handler
// registrati sul mux in InitHandlers.
// handleLogin, handleCallback, handleWebSocket, handleLongPolling, handleDownload, handleUpload
// rimangono invariati nella loro logica interna.

// handleLogin redirects the user to the Microsoft Entra ID login page.
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if !appConfig.EnableAuth {
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] handleLogin: Authentication disabled, redirecting to home.")
		}
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Println("[DEBUG] handleLogin: Initiating Azure AD login flow.")
	}

	loginURL, err := auth.GetLoginURL()
	if err != nil {
		log.Printf("Error retrieving login URL: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, loginURL, http.StatusFound)
}

// handleCallback handles the callback after authentication with Microsoft Entra ID.
func handleCallback(w http.ResponseWriter, r *http.Request) {
	if !appConfig.EnableAuth {
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] handleCallback: Authentication disabled, redirecting to home.")
		}
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Println("[DEBUG] handleCallback: Processing Azure AD callback.")
	}

	idToken, accessToken, err := auth.HandleCallback(r.Context(), r)
	if err != nil {
		log.Printf("Error handling authentication callback: %v", err)
		http.Error(w, fmt.Sprintf("Authentication error: %v", err), http.StatusInternalServerError)
		return
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Println("[DEBUG] handleCallback: ID Token and Access Token successfully retrieved.")
	}

	claims, err := auth.GetUserClaims(idToken)
	if err != nil {
		log.Printf("Error extracting base claims: %v", err)
		http.Error(w, "Error processing user data", http.StatusInternalServerError)
		return
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleCallback: Base claims extracted from ID Token for user: %s", claims.Email)
	}

	graphGroupIDs, graphGroupNames, err := auth.GetUserGroupsFromGraph(r.Context(), accessToken)
	if err != nil {
		log.Printf("Error getting user groups from Graph: %v", err)
		http.Error(w, fmt.Sprintf("Error retrieving user groups: %v", err), http.StatusInternalServerError)
		return
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleCallback: User group IDs retrieved from Microsoft Graph: %v", graphGroupIDs)
		log.Printf("[DEBUG] handleCallback: User group Names retrieved from Microsoft Graph: %v", graphGroupNames)
	}

	claims.Groups = graphGroupIDs
	claims.GroupNames = graphGroupNames
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleCallback: User claims updated with Graph groups. Final claims groups (IDs): %v", claims.Groups)
		log.Printf("[DEBUG] handleCallback: User claims updated with Graph groups. Final claims groups (Names): %v", claims.GroupNames)
	}


	if !auth.IsUserAuthorized(claims, appConfig) {
		log.Printf("User not authorized at application level during request: %s", claims.Email)
		http.Error(w, "Access denied: User not authorized to use the application", http.StatusForbidden)
		return
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleCallback: User '%s' is authorized at application level.", claims.Email)
	}


	secure := false
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		secure = true
	}

	claimsJSON, _ := json.Marshal(claims)
	cookie := &http.Cookie{
		Name:     "user_claims",
		Value:    url.QueryEscape(string(claimsJSON)),
		Path:     "/",
		Expires:  time.Now().Add(24 * time.Hour),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Println("[DEBUG] handleCallback: User claims stored in cookie.")
	}


	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Authentication successful for user: %s", claims.Email)
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("User %s authorized with groups (IDs): %v", claims.Email, claims.Groups)
			log.Printf("User %s authorized with groups (Names): %v", claims.GroupNames)
		}
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// AuthMiddleware is a middleware that applies user authentication and authorization checks.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] AuthMiddleware called for path: %s", r.URL.Path)
		}
		if !appConfig.EnableAuth {
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("[DEBUG] AuthMiddleware: Authentication disabled, bypassing checks.")
			}
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie("user_claims")
		if err != nil {
			if err == http.ErrNoCookie {
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Println("Session cookie missing, redirecting to login.")
				}
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}
			log.Printf("Error retrieving session cookie: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] AuthMiddleware: Session cookie found.")
		}


		claimsJSON, err := url.QueryUnescape(cookie.Value)
		if err != nil {
			log.Printf("Error decoding session cookie value: %v", err)
			http.Error(w, "Error processing user data", http.StatusInternalServerError)
			return
		}

		var claims auth.UserClaims
		if err := json.Unmarshal([]byte(claimsJSON), &claims); err != nil {
			log.Printf("Error parsing claims from cookie: %v", err)
			http.Error(w, "Error processing user data", http.StatusInternalServerError)
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			claimsDebug, _ := json.MarshalIndent(claims, "", "  ")
			log.Printf("[DEBUG] AuthMiddleware: Claims parsed from cookie:\n%s", string(claimsDebug))
			log.Printf("[DEBUG] AuthMiddleware: User's groups (IDs from cookie): %v", claims.Groups)
			log.Printf("[DEBUG] AuthMiddleware: User's groups (Names): %v", claims.GroupNames)
		}


		if !auth.IsUserAuthorized(&claims, appConfig) {
			log.Printf("User not authorized at application level during request: %s", claims.Email)
			http.Error(w, "Access denied: User not authorized to use the application", http.StatusForbidden)
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] AuthMiddleware: User '%s' is authorized for application access.", claims.Email)
		}


		ctx := context.WithValue(r.Context(), auth.ClaimsKey{}, &claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// getClaimsFromContext retrieves user claims from the request context.
// Returns nil if user authentication is disabled or user is not authenticated.
func getClaimsFromContext(ctx context.Context) (*auth.UserClaims, bool) {
	claims, ok := ctx.Value(auth.ClaimsKey{}).(*auth.UserClaims)
	return claims, ok
}

// handleWebSocket handles WebSocket connection requests after user authentication checks.
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	claims, _ := getClaimsFromContext(r.Context())
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleWebSocket: New WebSocket connection attempt. User claims present: %t", claims != nil)
		if claims != nil {
			log.Printf("[DEBUG] handleWebSocket: User email: %s", claims.Email)
			log.Printf("[DEBUG] handleWebSocket: User groups (IDs): %v", claims.Groups)
			log.Printf("[DEBUG] handleWebSocket: User groups (Names): %v", claims.GroupNames)
		}
	}
	wsHub.ServeWs(w, r, claims)
}

// handleLongPolling handles Long Polling requests after user authentication checks.
func handleLongPolling(w http.ResponseWriter, r *http.Request) {
	claims, _ := getClaimsFromContext(r.Context())
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleLongPolling: New Long Polling request. User claims present: %t", claims != nil)
		if claims != nil {
			log.Printf("[DEBUG] handleLongPolling: User email: %s", claims.Email)
			log.Printf("[DEBUG] handleLongPolling: User groups (IDs): %v", claims.Groups)
			log.Printf("[DEBUG] handleLongPolling: User groups (Names): %v", claims.GroupNames)
		}
	}


	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("Received Long Polling request: %s %s", r.Method, r.URL.Path)
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("Error reading Long Polling request body: %v", err)
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		} else {
			log.Printf("Long Polling request body: %s", string(bodyBytes))
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
	}

	wsHub.ServeLongPolling(w, r, claims)
}

// handleDownload handles file downloads via standard HTTP after user authentication checks.
func handleDownload(w http.ResponseWriter, r *http.Request) {
	claims, _ := getClaimsFromContext(r.Context())
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleDownload: Download request. User claims present: %t", claims != nil)
		if claims != nil {
			log.Printf("[DEBUG] handleDownload: User email: %s", claims.Email)
			log.Printf("[DEBUG] handleDownload: User groups (IDs): %v", claims.Groups)
			log.Printf("[DEBUG] handleDownload: User groups (Names): %v", claims.GroupNames)
		}
	}


	storageName := r.URL.Query().Get("storage")
	itemPath := r.URL.Query().Get("path")
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleDownload: Request for storage '%s', path '%s'", storageName, itemPath)
	}


	if storageName == "" || itemPath == "" {
		http.Error(w, "Parameters 'storage' and 'path' required", http.StatusBadRequest)
		return
	}

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
		log.Println("[DEBUG] handleDownload: Storage access granted.")
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
		} else if errors.Is(err, storage.ErrPermissionDenied) {
			http.Error(w, "Access denied: read permission required", http.StatusForbidden)
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
	}
}

// handleUpload manages file uploads via HTTP after user authentication checks.
func handleUpload(w http.ResponseWriter, r *http.Request) {
	claims, _ := getClaimsFromContext(r.Context())
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleUpload: Upload request. User claims present: %t", claims != nil)
		if claims != nil {
			log.Printf("[DEBUG] handleUpload: User email: %s", claims.Email)
			log.Printf("[DEBUG] handleUpload: User groups (IDs): %v", claims.Groups)
			log.Printf("[DEBUG] handleUpload: User groups (Names): %v", claims.GroupNames)
		}
	}


	contentType := r.Header.Get("Content-Type")
	log.Printf("Received upload request with Content-Type: %s", contentType)

	var err error
	const MAX_MEMORY = 400 << 20 // 400 MB - Adjust as needed

	// Parse multipart form for chunk uploads, or URL-encoded for initiate/finalize/cancel/status
	if strings.HasPrefix(contentType, "multipart/form-data") {
		err = r.ParseMultipartForm(MAX_MEMORY)
		if err != nil {
			log.Printf("Error parsing multipart form: %v", err)
			http.Error(w, "Error parsing multipart form", http.StatusBadRequest)
			return
		}
	} else if contentType == "application/x-www-form-urlencoded" {
		err = r.ParseForm()
		if err != nil {
			log.Printf("Error parsing urlencoded form: %v", err)
			http.Error(w, "Error parsing urlencoded form", http.StatusBadRequest)
			return
		}
	} else {
		log.Printf("Unsupported Content-Type for upload: %s", contentType)
		http.Error(w, "Unsupported Content-Type for upload", http.StatusBadRequest)
		return
	}

	storageName := r.FormValue("storage")
	itemPath := r.FormValue("path")
	action := r.FormValue("action")
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleUpload: Action '%s' for storage '%s', path '%s'", action, storageName, itemPath)
	}


	if storageName == "" || itemPath == "" || action == "" {
		log.Printf("Missing required parameters for upload after parsing: storage='%s', path='%s', action='%s'", storageName, itemPath, action)
		http.Error(w, "Parameters 'storage', 'path', and 'action' are required", http.StatusBadRequest)
		return
	}

	if err := authz.CheckStorageAccess(r.Context(), claims, storageName, itemPath, "write", appConfig); err != nil {
		if errors.Is(err, storage.ErrPermissionDenied) {
			http.Error(w, "Access denied: write permission required", http.StatusForbidden)
		} else {
			log.Printf("Error checking storage access for upload '%s/%s', action '%s': %v", storageName, itemPath, action, err)
			http.Error(w, "Internal server error during access check", http.StatusInternalServerError)
		}
		return
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Println("[DEBUG] handleUpload: Storage access granted for write operation.")
	}
	

	provider, ok := storage.GetProvider(storageName)
	if !ok {
		http.Error(w, "Storage provider not found", http.StatusNotFound)
		return
	} 

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleUpload: Provider %v",provider)
	}

	// Costruisci la chiave unica per il file in upload
	uploadKey := fmt.Sprintf("%s:%s", storageName, itemPath)

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] handleUpload:  uploadKey %v",uploadKey )
	}
	var currentUserEmail string
	if claims != nil {
		currentUserEmail = claims.Email
	} else {
		currentUserEmail = "unknown_user" // Fallback se i claims non sono disponibili (es. auth disabilitata)
	}


	switch action {
	case "initiate":
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] handleUpload: initiate action")
		}
		// LOGICA AGGIUNTA: Controllo upload concorrente
		wsHub.FileUploadsMutex.Lock() // Utilizza il campo esportato
		if sessionState, exists := wsHub.OngoingFileUploads[uploadKey]; exists { // Utilizza il campo esportato
			wsHub.FileUploadsMutex.Unlock() // Utilizza il campo esportato
			log.Printf("Upload conflict: File '%s' is already being uploaded by '%s'. Current user: '%s'", uploadKey, sessionState.Claims.Email, currentUserEmail)
			http.Error(w, fmt.Sprintf("File '%s' è già in fase di caricamento da parte di %s.", itemPath, sessionState.Claims.Email), http.StatusConflict)
			return
		}
		// Aggiungi il file alla mappa degli upload in corso
		// wsHub.OngoingFileUploads[uploadKey] = currentUserEmail // OLD: Sostituito con UploadSessionState
		
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Handling upload initiate for storage '%s', path '%s' by user '%s'", storageName, itemPath, currentUserEmail)
		}
		var uploadedSize int64
		var err error

		// Nuovi parametri per initiate: total_file_size e chunk_size
		totalFileSizeStr := r.FormValue("total_file_size")
		chunkSizeStr := r.FormValue("chunk_size")

		totalFileSize, parseErr1 := strconv.ParseInt(totalFileSizeStr, 10, 64)
		chunkSize, parseErr2 := strconv.ParseInt(chunkSizeStr, 10, 64)

		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("\t totalFileSize: %d\n\tchunkSize %d",totalFileSize,chunkSize)
		}

		if parseErr1 != nil || parseErr2 != nil || totalFileSize <= 0 || chunkSize <= 0 {
			wsHub.FileUploadsMutex.Lock()
			delete(wsHub.OngoingFileUploads, uploadKey)
			wsHub.FileUploadsMutex.Unlock()
			http.Error(w, "Missing or invalid total_file_size or chunk_size for initiate action", http.StatusBadRequest)
			return
		}

		switch p := provider.(type) {
		case *local.LocalFilesystemProvider:
			uploadedSize, err = p.InitiateUpload(r.Context(), claims, itemPath, totalFileSize, chunkSize)
		case *azureblob.AzureBlobStorageProvider:
			uploadedSize, err = p.InitiateUpload(r.Context(), claims, itemPath, totalFileSize, chunkSize)
		default:
			err = storage.ErrNotImplemented
		}

		if err != nil {
			// Rimuovi il file dalla mappa se l'inizializzazione fallisce
			wsHub.FileUploadsMutex.Lock() // Utilizza il campo esportato
			delete(wsHub.OngoingFileUploads, uploadKey) // Utilizza il campo esportato
			wsHub.FileUploadsMutex.Unlock() // Utilizza il campo esportato

			log.Printf("Error initiating upload for '%s/%s': %v", storageName, itemPath, err)
			if errors.Is(err, storage.ErrPermissionDenied) {
				http.Error(w, "Access denied: write permission required", http.StatusForbidden)
			} else if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "Destination not found", http.StatusNotFound)
			} else if errors.Is(err, storage.ErrNotImplemented) {
				http.Error(w, "Upload not supported for this storage type", http.StatusNotImplemented)
			} else {
				http.Error(w, fmt.Sprintf("Error initiating upload: %v", err), http.StatusInternalServerError)
			}
			return
		}

		// Store the upload session state in the Hub's map
		wsHub.FileUploadsMutex.Lock()
		wsHub.OngoingFileUploads[uploadKey] = &websocket.UploadSessionState{
			Claims:       claims, // Store claims for cleanup authorization
			StorageName:  storageName,
			ItemPath:     itemPath,
			LastActivity: time.Now(), // Set initial activity
			ProviderType: provider.Type(),
		}
		wsHub.FileUploadsMutex.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"uploaded_size": uploadedSize})

	case "chunk":
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] handleUpload: chunck action")
		}
		if !strings.HasPrefix(contentType, "multipart/form-data") {
			log.Printf("Received chunk action with incorrect Content-Type: %s", contentType)
			http.Error(w, "Chunk action requires multipart/form-data Content-Type", http.StatusBadRequest)
			return
		}

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Handling upload chunk for storage '%s', path '%s'", storageName, itemPath)
		}
		file, _, err := r.FormFile("chunk")
		if err != nil {
			log.Printf("Error getting file chunk for '%s/%s': %v", storageName, itemPath, err)
			http.Error(w, fmt.Sprintf("Error getting file chunk: %v", err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		blockID := r.FormValue("block_id") // Per Azure
		chunkIndexStr := r.FormValue("chunk_index") // Per locale
		chunkSizeStr := r.FormValue("chunk_size") // Per locale

		chunkIndex, parseErr1 := strconv.ParseInt(chunkIndexStr, 10, 64)
		chunkSize, parseErr2 := strconv.ParseInt(chunkSizeStr, 10, 64)

		if parseErr1 != nil || parseErr2 != nil || chunkSize <= 0 {
			http.Error(w, "Missing or invalid chunk_index or chunk_size for chunk action", http.StatusBadRequest)
			return
		}


		var writeErr error
		switch p := provider.(type) {
		case *local.LocalFilesystemProvider:
			chunkData, readErr := ioutil.ReadAll(file)
			if readErr != nil {
				log.Printf("Error reading file chunk for local upload '%s/%s': %v", storageName, itemPath, readErr)
				http.Error(w, fmt.Sprintf("Error reading file chunk: %v", readErr), http.StatusInternalServerError)
				return
			}
			writeErr = p.WriteChunk(r.Context(), claims, itemPath, chunkData, chunkIndex, chunkSize)
		case *azureblob.AzureBlobStorageProvider:
			if blockID == "" {
				http.Error(w, "Parameter 'block_id' is required for azure-blob chunk upload", http.StatusBadRequest)
				return
			}
			writeErr = p.WriteChunk(r.Context(), claims, itemPath, blockID, file, chunkIndex) // Passa chunkIndex anche per Azure
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

		// Update LastActivity for this upload session
		wsHub.FileUploadsMutex.Lock()
		if sessionState, exists := wsHub.OngoingFileUploads[uploadKey]; exists {
			sessionState.LastActivity = time.Now()
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Updated last activity for upload '%s' to %s", uploadKey, sessionState.LastActivity.Format(time.RFC3339))
			}
		}
		wsHub.FileUploadsMutex.Unlock()

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Successfully wrote chunk %d for storage '%s', path '%s'", chunkIndex, storageName, itemPath)
		}
		w.WriteHeader(http.StatusOK)

	case "finalize":
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Handling upload finalize for storage '%s', path '%s'", storageName, itemPath)
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] handleUpload: chunck finalize")
		}
		var err error
		var blockIDs []string
		clientSHA256 := r.FormValue("client_sha256")
		totalFileSizeStr := r.FormValue("total_file_size") // Recupera la dimensione totale del file

		totalFileSize, parseErr := strconv.ParseInt(totalFileSizeStr, 10, 64)
		if parseErr != nil || totalFileSize < 0 {
			http.Error(w, "Missing or invalid total_file_size for finalize action", http.StatusBadRequest)
			return
		}


		switch p := provider.(type) {
		case *local.LocalFilesystemProvider:
			err = p.FinalizeUpload(claims, itemPath, clientSHA256) // totalFileSize non è più necessario qui
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
			err = p.FinalizeUpload(r.Context(), claims, itemPath, blockIDs, clientSHA256)
		default:
			err = storage.ErrNotImplemented
		}

		// Rimuovi il file dalla mappa degli upload in corso dopo la finalizzazione
		wsHub.FileUploadsMutex.Lock() 
		delete(wsHub.OngoingFileUploads, uploadKey) 
		wsHub.FileUploadsMutex.Unlock() 

		if err != nil {
			log.Printf("Error finalizing upload for '%s/%s': %v", storageName, itemPath, err)
			if errors.Is(err, storage.ErrPermissionDenied) {
				http.Error(w, "Access denied: write permission required", http.StatusForbidden)
			} else if errors.Is(err, storage.ErrNotImplemented) {
				http.Error(w, "Upload finalization not supported for this storage type", http.StatusNotImplemented)
			} else if errors.Is(err, storage.ErrIntegrityCheckFailed) {
				http.Error(w, "File integrity check failed after upload. Hashes do not match.", http.StatusInternalServerError)
			} else {
				http.Error(w, fmt.Sprintf("Error finalizing upload: %v", err), http.StatusInternalServerError)
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
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] handleUpload: cancel action")
		}
		var err error

		switch p := provider.(type) {
		case *local.LocalFilesystemProvider:
			err = p.CancelUpload(claims, itemPath)
		case *azureblob.AzureBlobStorageProvider:
			err = p.CancelUpload(r.Context(), claims, itemPath)
		default:
			err = nil // Nessuna operazione di annullamento specifica per provider sconosciuti
		}

		// Rimuovi il file dalla mappa degli upload in corso dopo l'annullamento
		wsHub.FileUploadsMutex.Lock() 
		delete(wsHub.OngoingFileUploads, uploadKey) 
		wsHub.FileUploadsMutex.Unlock() 

		if err != nil {
			log.Printf("Error cancelling upload for '%s/%s': %v", storageName, itemPath, err)
			if errors.Is(err, storage.ErrPermissionDenied) {
				http.Error(w, "Access denied: write permission required", http.StatusForbidden)
			} else if !strings.Contains(err.Error(), "no ongoing upload session found") && !errors.Is(err, storage.ErrNotFound) {
				http.Error(w, fmt.Sprintf("Error cancelling upload: %v", err), http.StatusInternalServerError)
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
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] handleUpload: status action")
		}
		var uploadedSize int64
		var err error

		switch p := provider.(type) {
		case *local.LocalFilesystemProvider:
			uploadedSize, err = p.GetUploadedSize(claims, itemPath)
		case *azureblob.AzureBlobStorageProvider:
			uploadedSize, err = p.GetUploadedSize(r.Context(), claims, itemPath)
		default:
			uploadedSize = 0
			err = nil
		}

		if err != nil {
			log.Printf("Error getting upload status for '%s/%s': %v", storageName, itemPath, err)
			if errors.Is(err, storage.ErrPermissionDenied) {
				http.Error(w, "Access denied: read permission required", http.StatusForbidden)
			} else {
				http.Error(w, fmt.Sprintf("Error getting upload status: %v", err), http.StatusInternalServerError)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"uploaded_size": uploadedSize})

	default:
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] handleUpload: default action")
		}
		log.Printf("Received invalid upload action: %s for storage '%s', path '%s'", action, storageName, itemPath)
		http.Error(w, "Invalid upload action", http.StatusBadRequest)
	}
}
