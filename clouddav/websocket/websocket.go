package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"time"

	"clouddav/auth"
	"clouddav/config"
	"clouddav/internal/authz"
	"clouddav/storage"
	"clouddav/storage/azureblob"
	"clouddav/storage/local"

	"github.com/gorilla/websocket"
)

// Upgrader for WebSocket
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Client represents a single WebSocket/Long Polling client.
type Client struct {
	conn *websocket.Conn
	send chan Message
	mu   sync.Mutex
	isWS bool
	lastActivity time.Time
	claims *auth.UserClaims
	ctx    context.Context
	cancel context.CancelFunc
	userIdentifier string // Added to uniquely identify the user/client for cleanup
}

// UploadSessionState tracks the state of an ongoing file upload.
type UploadSessionState struct {
	Claims      *auth.UserClaims // Claims of the user who initiated the upload
	StorageName string           // Name of the storage provider
	ItemPath    string           // Path of the item being uploaded
	LastActivity time.Time       // Last time a chunk was received for this upload
	ProviderType string          // Type of the storage provider (e.g., "local", "azure-blob")
}

// Message represents a message sent or received via WebSocket/Long Polling.
type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
	RequestID string    `json:"request_id,omitempty"`
}

// Hub manages WebSocket and Long Polling clients.
type Hub struct {
	clients    map[*Client]bool
	register   chan *Client
	unregister   chan *Client
	broadcast  chan Message
	config *config.Config
	ctx    context.Context
	cancel context.CancelFunc

	// Mappa per tracciare gli upload di file in corso.
	// La chiave è "storageName:filePath", il valore è lo stato della sessione di upload.
	OngoingFileUploads map[string]*UploadSessionState // Key: "storageName:filePath", Value: *UploadSessionState
	FileUploadsMutex   sync.Mutex                     // Mutex per proteggere OngoingFileUploads
}

// NewHub creates a new Hub.
func NewHub(ctx context.Context, cfg *config.Config) *Hub {
	ctx, cancel := context.WithCancel(ctx)
	return &Hub{
		clients:            make(map[*Client]bool),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		broadcast:          make(chan Message),
		config:             cfg,
		ctx:                ctx,
		cancel:             cancel,
		OngoingFileUploads: make(map[string]*UploadSessionState), // Inizializza la mappa
		FileUploadsMutex:   sync.Mutex{},                         // Inizializza il mutex
	}
}

// Run starts the Hub, managing client registration/deregistration.
func (h *Hub) Run() {
	go h.cleanupLongPollingClients()
	go h.cleanupOrphanedUploads() // Avvia la goroutine per la pulizia degli upload orfani

	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Client registered. Total clients: %d", len(h.clients))
			}

			initialConfigMsg := Message{
				Type: "config_update",
				Payload: map[string]interface{}{
					"client_ping_interval_ms": h.config.ClientPingIntervalMs,
				},
			}
			go func() {
				select {
				case client.send <- initialConfigMsg:
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Sent initial config to new client (WS: %t)", client.isWS)
					}
				case <-time.After(5 * time.Second):
					log.Printf("Timeout sending initial config to new client (WS: %t)", client.isWS)
				case <-client.ctx.Done():
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Client context cancelled while sending initial config (WS: %t)", client.isWS)
					}
				}
			}()


		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				if client.conn != nil {
					client.conn.Close()
				}
				client.cancel() // Cancel client context

				// Identify uploads to cancel for this disconnected client
				// We now iterate through the OngoingFileUploads map to find uploads associated with this client
				uploadsToCancel := []struct{ UploadKey string; SessionState *UploadSessionState }{}
				h.FileUploadsMutex.Lock()
				for uploadKey, sessionState := range h.OngoingFileUploads {
					// Check if the upload's claims match the disconnected client's claims (or userIdentifier)
					// This assumes claims.Email is unique per user, or userIdentifier is unique per unauthenticated client session.
					if (client.claims != nil && sessionState.Claims != nil && client.claims.Email == sessionState.Claims.Email) ||
					   (client.claims == nil && sessionState.Claims == nil && client.userIdentifier == sessionState.Claims.Subject) { // Using Subject for anonymous ID
						uploadsToCancel = append(uploadsToCancel, struct{ UploadKey string; SessionState *UploadSessionState }{uploadKey, sessionState})
					}
				}
				// Remove them from the map immediately after identifying
				for _, upload := range uploadsToCancel {
					delete(h.OngoingFileUploads, upload.UploadKey)
				}
				h.FileUploadsMutex.Unlock()

				// Asynchronously cancel uploads on storage providers
				if len(uploadsToCancel) > 0 {
					log.Printf("Initiating cleanup for %d uploads from disconnected client '%s'", len(uploadsToCancel), client.userIdentifier)
					go func(uploads []struct{ UploadKey string; SessionState *UploadSessionState }) {
						for _, upload := range uploads {
							// Use the claims from the upload session state for authorization context
							claimsForCleanup := upload.SessionState.Claims
							
							provider, ok := storage.GetProvider(upload.SessionState.StorageName)
							if !ok {
								log.Printf("Warning: Storage provider '%s' not found during disconnected client cleanup for '%s'", upload.SessionState.StorageName, upload.SessionState.ItemPath)
								continue
							}
							var cancelErr error
							// Use a background context for server-side initiated cleanup
							// This ensures cleanup continues even if the original client context is cancelled.
							cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
							// It's important to defer cleanupCancel() inside this goroutine's loop
							// to ensure each cleanup operation has its own context and is cancelled.
							func() {
								defer cleanupCancel() 
								switch p := provider.(type) {
								case *local.LocalFilesystemProvider:
									cancelErr = p.CancelUpload(claimsForCleanup, upload.SessionState.ItemPath)
								case *azureblob.AzureBlobStorageProvider:
									cancelErr = p.CancelUpload(cleanupCtx, claimsForCleanup, upload.SessionState.ItemPath)
								default:
									log.Printf("Warning: CancelUpload not implemented for storage type '%s' during disconnected client cleanup.", provider.Type())
									return // Skip if not implemented
								}
								if cancelErr != nil {
									log.Printf("Error during cleanup of upload '%s' (storage: %s, path: %s) for disconnected client '%s': %v", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, client.userIdentifier, cancelErr)
								} else {
									log.Printf("Successfully cleaned up upload '%s' (storage: %s, path: %s) for disconnected client '%s'", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, client.userIdentifier)
								}
							}() // Call the anonymous function immediately
						}
					}(uploadsToCancel) 
				}

				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("Client unregistered. Total clients: %d", len(h.clients))
				}
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
					client.cancel()
				}
			}
		case <-h.ctx.Done():
			log.Println("Hub context cancelled, shutting down.")
			for client := range h.clients {
				h.unregister <- client
			}
			return
		}
	}
}

// cleanupLongPollingClients removes inactive Long Polling clients.
func (h *Hub) cleanupLongPollingClients() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			for client := range h.clients {
				if !client.isWS && now.Sub(client.lastActivity) > 60*time.Second {
					if config.IsLogLevel(config.LogLevelInfo) {
						log.Printf("Removing inactive Long Polling client")
					}
					h.unregister <- client
				}
			}
		case <-h.ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("Cleanup goroutine context cancelled, stopping.")
			}
			return
		}
	}
}

// cleanupOrphanedUploads periodically checks for and cancels orphaned uploads.
func (h *Hub) cleanupOrphanedUploads() {
	cleanupInterval := 1 * time.Minute // Check every minute
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	uploadCleanupTimeout, err := h.config.GetUploadCleanupTimeout()
	if err != nil {
		log.Printf("Error getting upload cleanup timeout from config, using default 10 minutes: %v", err)
		uploadCleanupTimeout = 10 * time.Minute // Fallback default
	}

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			uploadsToCancel := []struct{ UploadKey string; SessionState *UploadSessionState }{}

			h.FileUploadsMutex.Lock()
			for uploadKey, sessionState := range h.OngoingFileUploads {
				if now.Sub(sessionState.LastActivity) > uploadCleanupTimeout {
					log.Printf("Detected orphaned upload: %s (last activity: %s, timeout: %s)", uploadKey, sessionState.LastActivity.Format(time.RFC3339), uploadCleanupTimeout.String())
					uploadsToCancel = append(uploadsToCancel, struct{ UploadKey string; SessionState *UploadSessionState }{uploadKey, sessionState})
				}
			}
			// Remove identified orphaned uploads from the map
			for _, upload := range uploadsToCancel {
				delete(h.OngoingFileUploads, upload.UploadKey)
			}
			h.FileUploadsMutex.Unlock()

			if len(uploadsToCancel) > 0 {
				log.Printf("Initiating cleanup for %d orphaned uploads.", len(uploadsToCancel))
				go func(uploads []struct{ UploadKey string; SessionState *UploadSessionState }) {
					for _, upload := range uploads {
						claimsForCleanup := upload.SessionState.Claims
						
						provider, ok := storage.GetProvider(upload.SessionState.StorageName)
						if !ok {
							log.Printf("Warning: Storage provider '%s' not found during orphaned upload cleanup for '%s'", upload.SessionState.StorageName, upload.SessionState.ItemPath)
							continue
						}
						var cancelErr error
						cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
						func() {
							defer cleanupCancel()
							switch p := provider.(type) {
							case *local.LocalFilesystemProvider:
								cancelErr = p.CancelUpload(claimsForCleanup, upload.SessionState.ItemPath)
							case *azureblob.AzureBlobStorageProvider:
								cancelErr = p.CancelUpload(cleanupCtx, claimsForCleanup, upload.SessionState.ItemPath)
							default:
								log.Printf("Warning: CancelUpload not implemented for storage type '%s' during orphaned upload cleanup.", provider.Type())
								return
							}
							if cancelErr != nil {
								log.Printf("Error during cleanup of orphaned upload '%s' (storage: %s, path: %s): %v", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, cancelErr)
							} else {
								log.Printf("Successfully cleaned up orphaned upload '%s' (storage: %s, path: %s)", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath)
							}
						}()
					}
				}(uploadsToCancel)
			}

		case <-h.ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("Orphaned uploads cleanup goroutine context cancelled, stopping.")
			}
			return
		}
	}
}


// ServeWs handles WebSocket connection requests after user authentication checks.
func (h *Hub) ServeWs(w http.ResponseWriter, r *http.Request, claims *auth.UserClaims) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading to WebSocket: %v", err)
		http.Error(w, "Unable to establish WebSocket connection", http.StatusInternalServerError)
		return
	}

	clientCtx, clientCancel := context.WithCancel(h.ctx)

	// Determine user identifier for the client
	userIdent := "unauthenticated_client"
	if claims != nil {
		userIdent = claims.Email
	} else {
		// Generate a unique ID for unauthenticated clients (e.g., for anonymous access)
		userIdent = fmt.Sprintf("anon-%d", time.Now().UnixNano())
	}


	client := &Client{
		conn: conn,
		send: make(chan Message, 256),
		isWS: true,
		claims: claims,
		ctx: clientCtx,
		cancel: clientCancel,
		userIdentifier: userIdent, // Store the unique identifier
		lastActivity: time.Now(), // Initialize last activity
	}
	h.register <- client

	go client.writePump()
	go client.readPump(h)
}

// ServeLongPolling handles Long Polling requests after user authentication checks.
func (h *Hub) ServeLongPolling(w http.ResponseWriter, r *http.Request, claims *auth.UserClaims) {
	// Determine user identifier for the client
	userIdent := "unauthenticated_client"
	if claims != nil {
		userIdent = claims.Email
	} else {
		// For Long Polling, we need a way to identify the "client" across requests.
		// In a real-world scenario, you might use a session ID from a cookie.
		// For simplicity, we'll generate a new one per request if no claims,
		// but this means orphaned upload cleanup might be less precise for anonymous LP.
		// The primary cleanup for LP clients is via `cleanupLongPollingClients` based on `lastActivity` of the client itself.
		userIdent = fmt.Sprintf("anon-lp-%d", time.Now().UnixNano())
	}


	if r.Method == http.MethodPost {
		var msg Message
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&msg); err != nil {
			log.Printf("Error parsing Long Polling message: %v", err)
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LP Incoming Message (Server): Type=%s, RequestID=%s, Payload=%+v", msg.Type, msg.RequestID, msg.Payload)
		}

		reqCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		response, processErr := h.handleClientMessage(reqCtx, &msg, claims) // Pass claims to handleClientMessage
		if processErr != nil {
			log.Printf("Error processing Long Polling message: %v", processErr)
			response = Message{
				Type: "error",
				Payload: map[string]string{"error": processErr.Error()},
				RequestID: msg.RequestID,
			}
		}

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LP Outgoing Response (Server): Type=%s, RequestID=%s, Payload=%+v", response.Type, response.RequestID, response.Payload)
		}


		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("Error sending Long Polling response: %v", err)
		}

	} else if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		initialConfigMsg := Message{
			Type: "config_update",
			Payload: map[string]interface{}{
				"client_ping_interval_ms": h.config.ClientPingIntervalMs,
			},
		}
		json.NewEncoder(w).Encode(initialConfigMsg)

	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}

	_ = userIdent

}


// readPump reads messages from the WebSocket client and processes them.
func (c *Client) readPump(h *Hub) {
	defer func() {
		h.unregister <- c
	}()
	pongWait := 60 * time.Second
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		select {
		case <-c.ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Client context cancelled in readPump: %v", c.ctx.Err())
			}
			return
		default:
		}

		var msg Message
		err := c.conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			break
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("WebSocket message received: %+v", msg)
		}


		msgCtx, cancel := context.WithTimeout(c.ctx, 60*time.Second)

		go func(ctx context.Context, message Message) {
			defer cancel()

			response, processErr := h.handleClientMessage(ctx, &message, c.claims)
			if processErr != nil {
				log.Printf("Error processing message: %v", processErr)
				response = Message{
					Type: "error",
					Payload: map[string]string{"error": processErr.Error()},
					RequestID: message.RequestID,
				}
			}

			select {
			case c.send <- response:
			case <-c.ctx.Done():
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Client context cancelled while sending response: %v", c.ctx.Err())
				}
			}
		}(msgCtx, msg)
	}
}

// writePump sends messages to the WebSocket client.
func (c *Client) writePump() {
	pingPeriod := 60 * time.Second
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			c.mu.Lock()
			err := c.conn.WriteJSON(message)
			c.mu.Unlock()
			if err != nil {
				log.Printf("Error writing WebSocket: %v", err)
				return
			}
		case <-c.ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Client context cancelled in writePump: %v", c.ctx.Err())
			}
			c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		case <-ticker.C:
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.PingMessage, nil)
			c.mu.Unlock()
			if err != nil {
				log.Printf("Error sending Ping WebSocket: %v", err)
				return
			}
		}
	}
}

// handleClientMessage processes messages received from clients (WS or LP).
func (h *Hub) handleClientMessage(ctx context.Context, msg *Message, claims *auth.UserClaims) (Message, error) {
	var response Message
	response.Type = msg.Type + "_response"
	response.RequestID = msg.RequestID

	select {
	case <-ctx.Done():
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Context cancelled before processing message %s: %v", msg.Type, ctx.Err())
		}
		return response, ctx.Err()
	default:
	}


	switch msg.Type {
	case "get_filesystems":
		accessibleProviders := authz.GetAccessibleStorages(ctx, claims, h.config)
		response.Payload = accessibleProviders

	case "list_directory":
		var payload struct {
			StorageName     string `json:"storage_name"`
			DirPath         string `json:"dir_path"`
			Page            int    `json:"page"`
			ItemsPerPage    int    `json:"items_per_page"`
			NameFilter      string `json:"name_filter"`
			TimestampFilter string `json:"timestamp_filter"`
		}
		payloadBytes, err := json.Marshal(msg.Payload)
		if err != nil {
			return response, fmt.Errorf("failed to marshal payload for list_directory: %w", err)
		}
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			return response, fmt.Errorf("invalid list_directory payload: %w", err)
		}

		if err := authz.CheckStorageAccess(ctx, claims, payload.StorageName, payload.DirPath, "read", h.config); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: read permission required"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access for list_directory: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}

		itemsPerPage := h.config.Pagination.ItemsPerPage
		if payload.ItemsPerPage > 0 {
			itemsPerPage = payload.ItemsPerPage
		}

		page := payload.Page
		if page <= 0 {
			page = 1
		}

		var timestampFilter *time.Time
		if payload.TimestampFilter != "" {
			t, parseErr := time.Parse(time.RFC3339, payload.TimestampFilter)
			if parseErr != nil {
				log.Printf("Warning: Invalid timestamp filter format: %v", parseErr)
			} else {
				timestampFilter = &t
			}
		}

		listResponse, err := provider.ListItems(ctx, claims, payload.DirPath, page, itemsPerPage, payload.NameFilter, timestampFilter)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Directory not found"}
				return response, nil
			}
			return response, fmt.Errorf("error listing items from storage '%s': %w", payload.StorageName, err)
		}
		response.Payload = listResponse

	case "read_file":
		var payload struct {
			StorageName string `json:"storage_name"`
			ItemPath    string `json:"item_path"`
		}
		payloadBytes, err := json.Marshal(msg.Payload)
		if err != nil {
			return response, fmt.Errorf("failed to marshal payload for read_file: %w", err)
		}
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			return response, fmt.Errorf("invalid read_file payload: %w", err)
		}

		if err := authz.CheckStorageAccess(ctx, claims, payload.StorageName, payload.ItemPath, "read", h.config); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: read permission required"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access for read_file: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}

		reader, err := provider.OpenReader(ctx, claims, payload.ItemPath)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Item not found"}
				return response, nil
			} else if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: read permission required"}
				return response, nil
			} else {
				return response, fmt.Errorf("error opening item '%s/%s': %w", payload.StorageName, payload.ItemPath, err)
			}
		}
		defer reader.Close()

		content, err := ioutil.ReadAll(reader)
		if err != nil {
			select {
			case <-ctx.Done():
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Context cancelled during reading item '%s/%s': %v", payload.StorageName, payload.ItemPath, ctx.Err())
				}
				return response, ctx.Err()
			default:
			}
			return response, fmt.Errorf("error reading item content '%s/%s': %w", payload.StorageName, payload.ItemPath, err)
		}

		response.Payload = string(content)

	case "create_directory":
		var payload struct {
			StorageName string `json:"storage_name"`
			DirPath     string `json:"dir_path"`
		}
		payloadBytes, err := json.Marshal(msg.Payload)
		if err != nil {
			err = fmt.Errorf("failed to marshal payload for create_directory: %w", err)
			return response, err
		}
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			err = fmt.Errorf("invalid create_directory payload: %w", err)
			return response, err
		}

		if err := authz.CheckStorageAccess(ctx, claims, payload.StorageName, payload.DirPath, "write", h.config); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: write permission required"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access for create_directory: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}

		err = provider.CreateDirectory(ctx, claims, payload.DirPath)
		if err != nil {
			if errors.Is(err, storage.ErrAlreadyExists) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Directory already exists"}
				return response, nil
			} else if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: write permission required"}
				return response, nil
			} else if errors.Is(err, storage.ErrNotImplemented) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Create directory not supported for this storage type"}
				return response, nil
			}
			return response, fmt.Errorf("error creating directory '%s/%s': %w", payload.StorageName, payload.DirPath, err)
		}
		response.Payload = map[string]string{"status": "success"}

	case "delete_item":
		var payload struct {
			StorageName string `json:"storage_name"`
			ItemPath    string `json:"item_path"`
		}
		payloadBytes, err := json.Marshal(msg.Payload)
		if err != nil {
			err = fmt.Errorf("failed to marshal payload for delete_item: %w", err)
			return response, err
		}
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			err = fmt.Errorf("invalid delete_item payload: %w", err)
			return response, err
		}

		if err := authz.CheckStorageAccess(ctx, claims, payload.StorageName, payload.ItemPath, "write", h.config); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: write permission required"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access for delete_item: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}

		err = provider.DeleteItem(ctx, claims, payload.ItemPath)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Item not found"}
				return response, nil
			} else if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: write permission required"}
				return response, nil
			} else if errors.Is(err, storage.ErrNotImplemented) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Delete not supported for this storage type"}
				return response, nil
			}
			return response, fmt.Errorf("error deleting item '%s/%s': %w", payload.StorageName, payload.ItemPath, err)
		}
		response.Payload = map[string]string{"status": "success"}

	case "check_directory_contents_request":
		var payload struct {
			StorageName string `json:"storage_name"`
			DirPath     string `json:"dir_path"`
		}
		payloadBytes, err := json.Marshal(msg.Payload)
		if err != nil {
			return response, fmt.Errorf("failed to marshal payload for check_directory_contents_request: %w", err)
		}
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			return response, fmt.Errorf("invalid check_directory_contents_request payload: %w", err)
		}

		if err := authz.CheckStorageAccess(ctx, claims, payload.StorageName, payload.DirPath, "read", h.config); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: read permission required to check directory contents"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access for check_directory_contents_request: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}

		listResponse, err := provider.ListItems(ctx, claims, payload.DirPath, 1, 1, "", nil)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Payload = map[string]bool{"has_contents": false}
				return response, nil
			}
			return response, fmt.Errorf("error listing items to check directory contents: %w", err)
		}

		response.Payload = map[string]bool{"has_contents": listResponse.TotalItems > 0}
		return response, nil

	case "ping":
		response.Type = "pong"
		response.Payload = msg.Payload
		return response, nil

	case "config_update":
		log.Printf("Received unexpected config_update message from client: %+v", msg)
		response.Type = "error"
		response.Payload = map[string]string{"error": "unexpected message type"}
		return response, errors.New("unexpected message type: config_update")

	default:
		response.Type = "error"
		response.Payload = map[string]string{"error": fmt.Sprintf("unsupported message type: %s", msg.Type)}
		return response, fmt.Errorf("unsupported message type: %s", msg.Type)
	}

	select {
	case <-ctx.Done():
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Context cancelled after processing message %s: %v", msg.Type, ctx.Err())
		}
		return response, ctx.Err()
	default:
	}

	return response, nil
}
