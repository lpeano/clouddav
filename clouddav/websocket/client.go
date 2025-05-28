package websocket

import (
	"context"
	"errors"
	"io"
	"log"
	"net" // Aggiunto per net.Error
	"strings"
	"time"

	"clouddav/config" // Assicurati che il percorso sia corretto

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWaitClient = 10 * time.Second
	// Time allowed to read the next pong message from the peer.
	// Deve essere maggiore dell'intervallo di ping del client (config.ClientPingIntervalMs)
	pongWaitClientDefault = 60 * time.Second
	// Send pings to peer with this period. Must be less than pongWait.
	// Frequenza con cui il SERVER invia PING al client WebSocket.
	serverPingPeriodClientDefault = (pongWaitClientDefault * 9) / 10
	// Maximum message size allowed from peer.
	maxMessageSizeClient = 2048
)

// readPump pompa i messaggi dalla connessione WebSocket all'hub.
// È eseguita in una goroutine per client.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		// La chiusura della connessione WebSocket (c.conn.Close()) è gestita
		// centralmente in Hub.unregisterClient per evitare chiusure multiple.
	}()
	c.conn.SetReadLimit(maxMessageSizeClient)

	// Calcola pongWait basato sulla configurazione, altrimenti usa default.
	currentPongWait := pongWaitClientDefault
	if c.hub.config.ClientPingIntervalMs > 0 {
		calculatedPongWait := time.Duration(c.hub.config.ClientPingIntervalMs*3) * time.Millisecond
		if calculatedPongWait > 10*time.Second { // Assicura un minimo ragionevole
			currentPongWait = calculatedPongWait
		}
		// Corretta la chiamata a IsLogLevel
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("readPump (User: %s): ClientPingIntervalMs: %dms, Calculated PongWait: %s", c.userIdentifier, c.hub.config.ClientPingIntervalMs, currentPongWait)
		}
	}
	c.conn.SetReadDeadline(time.Now().Add(currentPongWait))

	c.conn.SetPongHandler(func(string) error {
		// Corretta la chiamata a IsLogLevel
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Pong ricevuto da client (User: %s)", c.userIdentifier)
		}
		c.conn.SetReadDeadline(time.Now().Add(currentPongWait))
		c.connMu.Lock()
		c.lastActivity = time.Now()
		c.connMu.Unlock()
		return nil
	})

	for {
		select {
		case <-c.ctx.Done(): // Se il contesto del client è cancellato (es. dall'hub)
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("readPump: Contesto client cancellato (User: %s), arresto readPump: %v", c.userIdentifier, c.ctx.Err())
			}
			return
		default:
			// Continua a leggere
		}

		var msg Message
		if err := c.conn.SetReadDeadline(time.Now().Add(currentPongWait)); err != nil {
			log.Printf("readPump: Errore impostazione ReadDeadline per client %s: %v", c.userIdentifier, err)
			return
		}

		err := c.conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNormalClosure) {
				log.Printf("Errore lettura WebSocket (User: %s): %v", c.userIdentifier, err)
			} else if err == io.EOF || errors.Is(err, websocket.ErrCloseSent) || strings.Contains(err.Error(), "use of closed network connection") {
				// Corretta la chiamata a IsLogLevel
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("Connessione WebSocket chiusa normally (User: %s): %v", c.userIdentifier, err)
				}
			} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Corretta la chiamata a IsLogLevel
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("Timeout lettura WebSocket (User: %s), nessuna attività o pong ricevuto: %v", c.userIdentifier, err)
				}
			} else {
				log.Printf("Errore non gestito lettura WebSocket (User: %s): %v", c.userIdentifier, err)
			}
			return
		}

		c.connMu.Lock()
		c.lastActivity = time.Now()
		c.connMu.Unlock()

		// Corretta la chiamata a IsLogLevel
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Messaggio WS ricevuto (User: %s): Type=%s, ReqID=%s, Payload=%+v", c.userIdentifier, msg.Type, msg.RequestID, msg.Payload)
		}

		msgProcessingCtx, cancelMsgProcessingCtx := context.WithTimeout(c.ctx, 60*time.Second)

		go func(ctx context.Context, message Message) {
			defer cancelMsgProcessingCtx()
			// Assicurati che HandleWebSocketMessage sia definito nel pacchetto websocket (probabilmente in message_handlers.go)
			// e che sia esportata (inizi con lettera maiuscola).
			response, procErr := HandleWebSocketMessage(ctx, c.hub, c, &message)
			if procErr != nil {
				log.Printf("Errore elaborazione messaggio (User: %s, Type: %s, ReqID: %s): %v", c.userIdentifier, message.Type, message.RequestID, procErr)
				response = Message{
					Type:      "error",
					Payload:   map[string]string{"error_type": "processing_error", "message": procErr.Error()},
					RequestID: message.RequestID,
				}
			}

			select {
			case c.send <- response:
				// Corretta la chiamata a IsLogLevel
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Risposta WS inviata (User: %s): Type=%s, ReqID=%s", c.userIdentifier, response.Type, response.RequestID)
				}
			case <-c.ctx.Done():
				// Corretta la chiamata a IsLogLevel
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Contesto client cancellato, impossibile inviare risposta (User: %s, Type: %s, ReqID: %s)", c.userIdentifier, response.Type, response.RequestID)
				}
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					log.Printf("Timeout elaborazione messaggio, impossibile inviare risposta (User: %s, Type: %s, ReqID: %s)", c.userIdentifier, response.Type, response.RequestID)
				} else {
					// Corretta la chiamata a IsLogLevel
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Contesto messaggio cancellato, impossibile inviare risposta (User: %s, Type: %s, ReqID: %s)", c.userIdentifier, response.Type, response.RequestID)
					}
				}
			}
		}(msgProcessingCtx, msg)
	}
}

// writePump pompa i messaggi dall'hub alla connessione WebSocket.
// È eseguita in una goroutine per client.
func (c *Client) writePump() {
	currentServerPingPeriod := serverPingPeriodClientDefault
	if c.hub.config.ClientPingIntervalMs > 0 {
		// Logica per adattare currentServerPingPeriod se necessario
	}
	if currentServerPingPeriod <= 0 {
		currentServerPingPeriod = 30 * time.Second
	}

	ticker := time.NewTicker(currentServerPingPeriod)
	defer func() {
		ticker.Stop()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.connMu.Lock()
			if c.conn == nil {
				c.connMu.Unlock()
				// Corretta la chiamata a IsLogLevel
				if ok && config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("writePump: Connessione WebSocket per client %s è nil, messaggio non inviato: %s", c.userIdentifier, message.Type)
				}
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(writeWaitClient))
			c.connMu.Unlock()

			if !ok {
				// Corretta la chiamata a IsLogLevel
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("Canale 'send' chiuso per client (User: %s). Chiusura writePump.", c.userIdentifier)
				}
				c.connMu.Lock()
				if c.conn != nil {
					_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				}
				c.connMu.Unlock()
				return
			}

			c.connMu.Lock()
			err := c.conn.WriteJSON(message)
			c.connMu.Unlock()

			if err != nil {
				log.Printf("Errore scrittura WebSocket (User: %s): %v", c.userIdentifier, err)
				if strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
			}
		case <-ticker.C:
			c.connMu.Lock()
			if c.conn == nil {
				c.connMu.Unlock()
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(writeWaitClient))
			err := c.conn.WriteMessage(websocket.PingMessage, nil)
			c.connMu.Unlock()

			if err != nil {
				log.Printf("Errore invio Ping WebSocket (User: %s): %v", c.userIdentifier, err)
				return
			}
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Ping inviato a client (User: %s)", c.userIdentifier)
			}
		case <-c.ctx.Done():
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("writePump: Contesto client cancellato (User: %s), arresto writePump: %v", c.userIdentifier, c.ctx.Err())
			}
			return
		}
	}
}
