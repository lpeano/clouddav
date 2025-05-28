// Si assume che l'oggetto 'config' con 'LogLevelDebug', 'LogLevelInfo', 
// 'currentLogLevel' e 'IsLogLevel' sia già stato definito globalmente
// in un file caricato precedentemente (es. config.js o all'inizio di app_logic.js).
//
// Esempio di come dovrebbe essere definito config globalmente (in config.js):
//
// window.config = {
//     LogLevelDebug: 'DEBUG',
//     LogLevelInfo: 'INFO',
//     LogLevelWarning: 'WARNING',
//     LogLevelError: 'ERROR',
//     currentLogLevel: 'DEBUG', // o 'INFO' in produzione
//     IsLogLevel: function(levelToCheck) {
//         if (!this.currentLogLevel) return false; 
//         const levels = { 
//             [this.LogLevelDebug]: 1, 
//             [this.LogLevelInfo]: 2, 
//             [this.LogLevelWarning]: 3, 
//             [this.LogLevelError]: 4 
//         };
//         const current = levels[this.currentLogLevel];
//         const toCheck = levels[levelToCheck];
//         if (current === undefined || toCheck === undefined) return false; 
//         return toCheck >= current;
//     }
// };

// --------------- websocket_service.js ---------------
const websocket_service_module = (() => {
    let ws;
    let requestCallbacks = new Map(); 
    let messageQueue = []; // DEFINIZIONE DI messageQueue
    let isConnecting = false;
    let reconnectAttempts = 0;
    const MAX_RECONNECT_ATTEMPTS = 5;
    const RECONNECT_DELAY_BASE_MS = 2000; 
    const REQUEST_TIMEOUT_MS = 30000; 

    function generateRequestIDInternal() {
        return ([1e7]+-1e3+-4e3+-8e3+-1e11).replace(/[018]/g, c =>
            (c ^ crypto.getRandomValues(new Uint8Array(1))[0] & 15 >> c / 4).toString(16)
        );
    }
    
    function updateStatusUI(statusType, message) {
        if (window.app_logic && typeof window.app_logic.updateWebSocketStatusOnMainPage === 'function') { 
            window.app_logic.updateWebSocketStatusOnMainPage(statusType, message);
        } else {
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelWarning)) {
                console.warn("WebSocketService: window.app_logic.updateWebSocketStatusOnMainPage non definita.");
            }
        }

        if (window.app_logic && typeof window.app_logic.addMessageToHistory === 'function') { 
            let logType = 'info';
            if (statusType.includes('error') || statusType.includes('failed') || statusType.includes('closed_abnormally')) logType = 'error';
            if (statusType.includes('connecting') || statusType.includes('reconnecting') || statusType.includes('closed_will_retry')) logType = 'warning';
            window.app_logic.addMessageToHistory(`WebSocket: ${message}`, logType);
        }
    }

    // DEFINIZIONE di processMessageQueue
    function processMessageQueue() {
        if (ws && ws.readyState === WebSocket.OPEN) {
            while (messageQueue.length > 0) { 
                const queuedMessage = messageQueue.shift(); 
                try {
                    ws.send(JSON.stringify(queuedMessage));
                    if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                        console.debug("WebSocketService: Messaggio dalla coda inviato:", queuedMessage);
                    }
                } catch (e) {
                    console.error("WebSocketService: Errore invio messaggio dalla coda:", e, queuedMessage);
                    if (queuedMessage.request_id && requestCallbacks.has(queuedMessage.request_id)) {
                        const handlers = requestCallbacks.get(queuedMessage.request_id);
                        clearTimeout(handlers.timeoutTimer);
                        handlers.onError({ error: "Failed to send queued message: " + e.message, error_type: "send_queue_exception" });
                        requestCallbacks.delete(queuedMessage.request_id);
                    }
                }
            }
        }
    }

    function connect() {
        if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                console.debug("WebSocketService: Tentativo di connessione già in corso o stabilita.");
            }
            return;
        }
        isConnecting = true;
        reconnectAttempts++;
        updateStatusUI('ws_connecting', `Tentativo connessione WebSocket (${reconnectAttempts} di ${MAX_RECONNECT_ATTEMPTS})...`);
        
        const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${wsProtocol}//${window.location.host}/ws`;
        
        try {
            ws = new WebSocket(wsUrl);
        } catch (e) {
            console.error("WebSocketService: Errore creazione istanza WebSocket:", e);
            isConnecting = false;
            updateStatusUI('ws_error', 'Errore creazione WebSocket.');
            if (reconnectAttempts < MAX_RECONNECT_ATTEMPTS) {
                const delay = Math.min(Math.pow(2, reconnectAttempts) * RECONNECT_DELAY_BASE_MS, 30000); 
                console.log(`WebSocketService: Prossimo tentativo di riconnessione tra ${delay / 1000} secondi...`);
                setTimeout(connect, delay);
            } else {
                updateStatusUI('ws_failed_reconnect', 'Impossibile stabilire connessione WebSocket dopo vari tentativi.');
            }
            return;
        }

        ws.onopen = () => {
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                console.info("WebSocketService: Connessione stabilita.");
            }
            updateStatusUI('ws_established', 'Connessione WebSocket attiva.');
            isConnecting = false;
            reconnectAttempts = 0;
            processMessageQueue(); // Invia messaggi in coda dopo che la connessione è aperta
        };

        ws.onmessage = (event) => {
            try {
                const response = JSON.parse(event.data);
                if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                    console.debug("WebSocketService: Messaggio ricevuto dal server:", JSON.stringify(response, null, 2));
                }

                if (response.request_id && requestCallbacks.has(response.request_id)) {
                    const handlers = requestCallbacks.get(response.request_id);
                    clearTimeout(handlers.timeoutTimer); 

                    if (response.type && (response.type === 'error' || response.type.endsWith("_error") || response.type.endsWith("Response_error"))) { 
                        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                            console.debug(`WebSocketService: Invocazione onError per ReqID ${response.request_id}`);
                        }
                        handlers.onError(response.payload);
                    } else {
                        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                            console.debug(`WebSocketService: Invocazione onSuccess per ReqID ${response.request_id}`);
                        }
                        handlers.onSuccess(response.payload); 
                    }
                    requestCallbacks.delete(response.request_id);
                } else if (response.type === 'config_update') {
                     if (window.app_logic && window.app_logic.handleConfigUpdate) window.app_logic.handleConfigUpdate(response.payload); 
                } else {
                    console.warn("WebSocketService: Ricevuto messaggio non sollecitato o senza request_id corrispondente:", response);
                    if (window.app_logic && window.app_logic.handleBackendMessage) window.app_logic.handleBackendMessage(response); 
                }
            } catch (e) {
                console.error("WebSocketService: Errore parsing messaggio JSON dal server:", e, event.data);
            }
        };

        ws.onerror = (error) => {
            console.error("WebSocketService: Errore WebSocket:", error);
        };

        ws.onclose = (event) => {
            isConnecting = false;
            ws = null; 
            
            console.warn(`WebSocketService: Connessione chiusa. Codice: ${event.code}, Motivo: '${event.reason || "N/A"}', Pulita: ${event.wasClean}`);
            
            requestCallbacks.forEach((handlers, requestID) => {
                clearTimeout(handlers.timeoutTimer);
                handlers.onError({ error: "WebSocket connection closed", error_type: "disconnect", code: event.code });
                if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelWarning)) {
                    console.warn(`WebSocketService: Chiamata onError per richiesta pendente ${requestID} a causa della chiusura della connessione.`);
                }
            });
            requestCallbacks.clear();

            if (event.code === 1000 || event.code === 1001 ) { 
                 updateStatusUI('ws_closed_normally', `Connessione WebSocket chiusa (Codice: ${event.code}).`);
                 reconnectAttempts = MAX_RECONNECT_ATTEMPTS; 
            } else if (reconnectAttempts < MAX_RECONNECT_ATTEMPTS) {
                const delay = Math.min(Math.pow(2, reconnectAttempts) * RECONNECT_DELAY_BASE_MS, 30000); 
                updateStatusUI('ws_closed_will_retry', `Connessione WebSocket persa (Codice: ${event.code}). Riconnessione tra ${delay / 1000}s...`);
                if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                    console.info(`WebSocketService: Tentativo di riconnessione tra ${delay / 1000} secondi...`);
                }
                setTimeout(connect, delay);
            } else {
                console.error("WebSocketService: Massimo numero di tentativi di riconnessione raggiunto.");
                updateStatusUI('ws_failed_reconnect', 'Impossibile riconnettersi al server WebSocket.');
            }
        };
    }
    
    return {
        connect: connect,
        sendMessage: (message) => { 
            if (!message.request_id) {
                message.request_id = generateRequestIDInternal();
            }
            
            if (!ws || ws.readyState !== WebSocket.OPEN) {
                if (isConnecting || reconnectAttempts < MAX_RECONNECT_ATTEMPTS) {
                    if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                        console.info(`WebSocketService: Connessione non pronta (stato: ${ws ? ws.readyState : 'non inizializzata'}, isConnecting: ${isConnecting}). Messaggio accodato:`, message);
                    }
                    messageQueue.push(message); // UTILIZZO DI messageQueue
                } else {
                     const errorMsg = "WebSocketService: Impossibile inviare messaggio, connessione WebSocket non disponibile e tentativi massimi raggiunti.";
                     console.error(errorMsg, message);
                     if (requestCallbacks.has(message.request_id)) {
                        const handlers = requestCallbacks.get(message.request_id);
                        clearTimeout(handlers.timeoutTimer);
                        handlers.onError({ error: errorMsg, error_type: "connection_unavailable" });
                        requestCallbacks.delete(message.request_id);
                     }
                     return null; 
                }
                return message.request_id; 
            }
            
            // Se la connessione è aperta, invia direttamente
            try {
                ws.send(JSON.stringify(message));
                 if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                    console.debug("WebSocketService: Messaggio inviato direttamente:", message);
                }
            } catch (e) {
                console.error("WebSocketService: Errore durante ws.send:", e, message);
                if(requestCallbacks.has(message.request_id)){
                    const handlers = requestCallbacks.get(message.request_id);
                    clearTimeout(handlers.timeoutTimer);
                    handlers.onError({error: "Failed to send message: " + e.message, error_type: "send_exception"});
                    requestCallbacks.delete(message.request_id);
                }
                return null; 
            }
            return message.request_id;
        },
        registerCallbackForRequestID: (requestID, onSuccess, onError, timeoutMs = REQUEST_TIMEOUT_MS) => {
            if (!requestID) {
                console.error("WebSocketService: Tentativo di registrare callback senza requestID.");
                if (onError) onError({error: "Internal error: No requestID for callback.", error_type: "internal"});
                return;
            }
            const timeoutTimer = setTimeout(() => {
                if (requestCallbacks.has(requestID)) {
                    if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelWarning)) {
                        console.warn(`WebSocketService: Timeout per requestID ${requestID} dopo ${timeoutMs}ms`);
                    }
                    requestCallbacks.get(requestID).onError({ error: "Request timeout", error_type: "timeout" });
                    requestCallbacks.delete(requestID);
                }
            }, timeoutMs);
            requestCallbacks.set(requestID, { onSuccess, onError, timeoutTimer });
        }
    };
})();

window.websocket_service = websocket_service_module;

if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
    console.info("websocket_service.js eseguito. window.websocket_service:", window.websocket_service);
}

window.websocket_service_ready_flag = true; 
const eventReady = new CustomEvent('websocketServiceReady');
window.dispatchEvent(eventReady);

if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
    console.info("WebSocketService: Flag 'websocket_service_ready_flag' impostato e evento 'websocketServiceReady' emesso.");
}

