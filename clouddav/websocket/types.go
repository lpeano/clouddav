package websocket

import (
	"clouddav/auth" // Assicurati che il percorso del pacchetto auth sia corretto
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Message rappresenta un messaggio inviato o ricevuto via WebSocket/Long Polling.
type Message struct {
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	RequestID string      `json:"request_id,omitempty"`
}

// Client rappresenta un singolo client WebSocket o Long Polling.
// I metodi readPump e writePump sono stati spostati in client.go
type Client struct {
	hub *Hub
	// La connessione WebSocket. Nil se il client è in Long Polling.
	conn *websocket.Conn
	// Canale bufferizzato per i messaggi in uscita.
	send chan Message
	// Mutex per proteggere la scrittura sulla connessione WebSocket (conn).
	connMu sync.Mutex
	// True se la connessione corrente è WebSocket.
	isWS bool
	// Ultima attività per client Long Polling (per timeout).
	lastActivity time.Time
	// Claims dell'utente autenticato.
	claims *auth.UserClaims // Assicurati che auth.UserClaims sia il tipo corretto
	// Contesto del client, derivato dall'Hub.
	ctx context.Context
	// Funzione per cancellare il contesto del client.
	cancel context.CancelFunc
	// Identificatore univoco per il client (email o ID generato).
	userIdentifier string
}

// UploadSessionState tiene traccia dello stato di un upload di file in corso.
type UploadSessionState struct {
	Claims         *auth.UserClaims // Assicurati che auth.UserClaims sia il tipo corretto
	StorageName    string
	ItemPath       string
	LastActivity   time.Time
	ProviderType   string
	// Aggiungi qui altri campi specifici per lo stato dell'upload se necessario
}
