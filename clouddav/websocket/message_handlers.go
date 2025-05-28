package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"clouddav/config"
	"clouddav/internal/authz"
	"clouddav/storage"
)

// HandleWebSocketMessage è il dispatcher principale per i messaggi WebSocket/LP.
// Prende il contesto del client per rispettare la sua cancellazione.
func HandleWebSocketMessage(clientCtx context.Context, hub *Hub, client *Client, msg *Message) (Message, error) {
	var response Message
	response.Type = msg.Type + "_response"
	response.RequestID = msg.RequestID

	opCtx, opCancel := context.WithTimeout(clientCtx, 30*time.Second)
	defer opCancel()

	switch msg.Type {
	case "get_filesystems": // NUOVO CASE AGGIUNTO
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Richiesta get_filesystems (User: %s, ReqID: %s)", client.userIdentifier, msg.RequestID)
		}
		// La funzione GetAccessibleStorages si aspetta il contesto della richiesta, i claims dell'utente e la configurazione.
		// Non è necessario un payload specifico dal client per questa richiesta.
		accessibleStorages := authz.GetAccessibleStorages(opCtx, client.claims, hub.config)
		
		response.Payload = accessibleStorages
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Risposta get_filesystems (User: %s, ReqID: %s): Trovati %d storage accessibili", client.userIdentifier, msg.RequestID, len(accessibleStorages))
		}

	case "list_directory":
		var p struct {
			StorageName     string `json:"storage_name"`
			DirPath         string `json:"dir_path"`
			Page            int    `json:"page"`
			ItemsPerPage    int    `json:"items_per_page"`
			NameFilter      string `json:"name_filter,omitempty"`
			TimestampFilter string `json:"timestamp_filter,omitempty"`
			OnlyDirectories bool   `json:"only_directories,omitempty"`
		}
		if err := mapToStruct(msg.Payload, &p); err != nil {
			return createErrorResponse(msg.RequestID, "payload_parse_error", fmt.Sprintf("Errore nel parsing del payload per list_directory: %v", err)), err
		}

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Richiesta list_directory: Storage=%s, Path=%s, Page=%d, ItemsPerPage=%d, NameFilter='%s', TimestampFilter='%s', OnlyDirs=%t",
				p.StorageName, p.DirPath, p.Page, p.ItemsPerPage, p.NameFilter, p.TimestampFilter, p.OnlyDirectories)
		}
		
		if err := authz.CheckStorageAccess(opCtx, client.claims, p.StorageName, p.DirPath, "read", hub.config); err != nil {
			return createErrorResponse(msg.RequestID, "auth_error", fmt.Sprintf("Accesso negato a %s/%s: %v", p.StorageName, p.DirPath, err)), err
		}

		provider, ok := storage.GetProvider(p.StorageName)
		if !ok {
			return createErrorResponse(msg.RequestID, "storage_error", fmt.Sprintf("Provider di storage '%s' non trovato.", p.StorageName)), fmt.Errorf("provider di storage '%s' non trovato", p.StorageName)
		}

		var tFilter *time.Time
		if p.TimestampFilter != "" {
			parsedTime, err := time.Parse(time.RFC3339, p.TimestampFilter)
			if err == nil {
				tFilter = &parsedTime
			} else {
				log.Printf("Formato timestamp non valido per il filtro: %s, errore: %v", p.TimestampFilter, err)
			}
		}
		
		listResp, err := provider.ListItems(opCtx, client.claims, p.DirPath, p.Page, p.ItemsPerPage, p.NameFilter, tFilter, p.OnlyDirectories)
		if err != nil {
			log.Printf("Errore ListItems per %s/%s (User: %s): %v", p.StorageName, p.DirPath, client.userIdentifier, err)
			return createErrorResponse(msg.RequestID, "list_error", fmt.Sprintf("Errore nel listare la directory: %v", err)), err
		}
		
		response.Payload = struct {
			*storage.ListItemsResponse
			StorageName string `json:"storage_name"`
			DirPath     string `json:"dir_path"`
		}{
			ListItemsResponse: listResp,
			StorageName:       p.StorageName,
			DirPath:           p.DirPath,
		}


	case "create_directory":
		var p struct {
			StorageName string `json:"storage_name"`
			DirPath     string `json:"dir_path"`
		}
		if err := mapToStruct(msg.Payload, &p); err != nil {
			return createErrorResponse(msg.RequestID, "payload_parse_error", fmt.Sprintf("Errore nel parsing del payload per create_directory: %v", err)), err
		}

		parentDir := filepath.Dir(strings.TrimSuffix(p.DirPath, "/"))
		if parentDir == "." {
			parentDir = ""
		}

		if err := authz.CheckStorageAccess(opCtx, client.claims, p.StorageName, parentDir, "write", hub.config); err != nil {
			return createErrorResponse(msg.RequestID, "auth_error", fmt.Sprintf("Permesso di scrittura negato per creare directory in %s/%s: %v", p.StorageName, parentDir, err)), err
		}

		provider, ok := storage.GetProvider(p.StorageName)
		if !ok {
			return createErrorResponse(msg.RequestID, "storage_error", fmt.Sprintf("Provider di storage '%s' non trovato.", p.StorageName)), fmt.Errorf("provider di storage '%s' non trovato", p.StorageName)
		}
		err := provider.CreateDirectory(opCtx, client.claims, p.DirPath)
		if err != nil {
			log.Printf("Errore CreateDirectory per %s/%s (User: %s): %v", p.StorageName, p.DirPath, client.userIdentifier, err)
			return createErrorResponse(msg.RequestID, "create_dir_error", fmt.Sprintf("Errore creazione directory: %v", err)), err
		}
		response.Payload = map[string]string{"status": "success", "dir_path": p.DirPath, "name": filepath.Base(p.DirPath)}

	case "delete_item":
		var p struct {
			StorageName string `json:"storage_name"`
			ItemPath    string `json:"item_path"`
		}
		if err := mapToStruct(msg.Payload, &p); err != nil {
			return createErrorResponse(msg.RequestID, "payload_parse_error", fmt.Sprintf("Errore nel parsing del payload per delete_item: %v", err)), err
		}

		itemParentDir := filepath.Dir(strings.TrimSuffix(p.ItemPath, "/"))
		if itemParentDir == "." {
			itemParentDir = ""
		}

		if err := authz.CheckStorageAccess(opCtx, client.claims, p.StorageName, itemParentDir, "write", hub.config); err != nil {
			if errAlt := authz.CheckStorageAccess(opCtx, client.claims, p.StorageName, p.ItemPath, "write", hub.config); errAlt != nil {
				return createErrorResponse(msg.RequestID, "auth_error", fmt.Sprintf("Permesso di scrittura negato per eliminare %s/%s: %v", p.StorageName, p.ItemPath, err)), err
			}
		}

		provider, ok := storage.GetProvider(p.StorageName)
		if !ok {
			return createErrorResponse(msg.RequestID, "storage_error", fmt.Sprintf("Provider di storage '%s' non trovato.", p.StorageName)), fmt.Errorf("provider di storage '%s' non trovato", p.StorageName)
		}
		err := provider.DeleteItem(opCtx, client.claims, p.ItemPath)
		if err != nil {
			log.Printf("Errore DeleteItem per %s/%s (User: %s): %v", p.StorageName, p.ItemPath, client.userIdentifier, err)
			return createErrorResponse(msg.RequestID, "delete_item_error", fmt.Sprintf("Errore eliminazione item: %v", err)), err
		}
		response.Payload = map[string]string{"status": "success", "item_path": p.ItemPath, "name": filepath.Base(p.ItemPath)}
	
	case "check_directory_contents_request":
		var p struct {
			StorageName string `json:"storage_name"`
			DirPath     string `json:"dir_path"`
		}
		if err := mapToStruct(msg.Payload, &p); err != nil {
			return createErrorResponse(msg.RequestID, "payload_parse_error", fmt.Sprintf("Errore parsing payload per check_directory_contents: %v", err)), err
		}

		if err := authz.CheckStorageAccess(opCtx, client.claims, p.StorageName, p.DirPath, "read", hub.config); err != nil {
			return createErrorResponse(msg.RequestID, "auth_error", fmt.Sprintf("Accesso negato a %s/%s: %v", p.StorageName, p.DirPath, err)), err
		}
		
		provider, ok := storage.GetProvider(p.StorageName)
		if !ok {
			return createErrorResponse(msg.RequestID, "storage_error", fmt.Sprintf("Provider di storage '%s' non trovato.", p.StorageName)), fmt.Errorf("provider di storage '%s' non trovato", p.StorageName)
		}
		
		listResp, err := provider.ListItems(opCtx, client.claims, p.DirPath, 1, 1, "", nil, false)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Payload = map[string]interface{}{"has_contents": false, "dir_path": p.DirPath}
				return response, nil
			}
			log.Printf("Errore ListItems per check_directory_contents %s/%s (User: %s): %v", p.StorageName, p.DirPath, client.userIdentifier, err)
			return createErrorResponse(msg.RequestID, "check_contents_error", fmt.Sprintf("Errore nel controllare contenuto directory: %v", err)), err
		}
		response.Payload = map[string]interface{}{"has_contents": listResp.TotalItems > 0, "dir_path": p.DirPath}


	case "ping":
		response.Type = "pong"
		response.Payload = msg.Payload
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Ping ricevuto (User: %s, ReqID: %s), invio Pong.", client.userIdentifier, msg.RequestID)
		}

	default:
		errMsg := fmt.Sprintf("Tipo di messaggio non supportato: %s", msg.Type)
		log.Println(errMsg)
		return createErrorResponse(msg.RequestID, "unsupported_type", errMsg), errors.New(errMsg)
	}

	return response, nil
}

// mapToStruct converte una mappa (tipicamente da json.RawMessage o interface{}) in una struct.
func mapToStruct(data interface{}, result interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("errore nel marshalling del payload: %w", err)
	}
	if err := json.Unmarshal(jsonData, result); err != nil {
		return fmt.Errorf("errore nell'unmarshalling del payload in struct: %w", err)
	}
	return nil
}

// createErrorResponse crea un messaggio di errore standard.
func createErrorResponse(requestID, errorType, errorMessage string) Message {
	return Message{
		Type:      "error",
		Payload:   map[string]string{"error_type": errorType, "message": errorMessage},
		RequestID: requestID,
	}
}
