// static/js/websocket_service.js
// Manages WebSocket connection and Long Polling fallback.

(function() {
    let ws;
    let isWebSocketActive = false;
    let messageQueue = [];
    let isProcessingQueue = false;
    let longPollingIntervalId = null;
    const longPollingUrl = '/lp'; // Endpoint for Long Polling

    const websocketProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const websocketUrl = `${websocketProtocol}//${window.location.host}/ws`;

    const pingTimestamps = new Map();
    let serverPingIntervalMs = 10000; // Default, will be updated by server
    let clientPingIntervalId = null;

    function notifyConnectionStatusToApp(status, details = {}) {
        // Call the global function defined in app_logic.js (or index.html)
        if (window.updateWebSocketStatusOnMainPage) {
            window.updateWebSocketStatusOnMainPage(status, details.message || status);
        }
         // Also log to message history via app_logic.js
        if (window.addMessageToHistory) {
            let historyMsg = `Stato Connessione: ${status}`;
            if(details.message) historyMsg += ` - ${details.message}`;
            if(details.code) historyMsg += ` (Code: ${details.code})`;
            if(details.reason) historyMsg += ` (Reason: ${details.reason})`;
            window.addMessageToHistory(historyMsg, status.includes('error') || status === 'lp_fallback' ? 'warning' : 'info');
        }
    }
    
    function connectWebSocketInternal() {
        notifyConnectionStatusToApp('ws_connecting', {message: 'Tentativo connessione WebSocket...'});
        ws = new WebSocket(websocketUrl);

        ws.onopen = () => {
            console.log('WebSocket connection established.');
            notifyConnectionStatusToApp('ws_established', {message: 'Connessione WebSocket attiva'});
            isWebSocketActive = true;
            processMessageQueueInternal();
            stopLongPollingInternal();
            startClientPingInterval();
        };

        ws.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data);
                if (window.handleBackendMessage) { // Defined in app_logic.js
                    window.handleBackendMessage(message);
                } else {
                    console.warn('WebSocketService - window.handleBackendMessage not defined.');
                }
            } catch (e) {
                console.error('WebSocketService - Error parsing message:', e);
                if (window.addMessageToHistory) window.addMessageToHistory(`Errore parsing messaggio WebSocket: ${e.message}`, 'error');
            }
        };

        ws.onerror = (error) => {
            console.error('WebSocket error:', error);
            notifyConnectionStatusToApp('ws_error', { message: 'Errore WebSocket.', error: error.message });
            // onclose will handle fallback
        };

        ws.onclose = (event) => {
            console.log('WebSocket connection closed:', event.code, event.reason);
            isWebSocketActive = false;
            notifyConnectionStatusToApp('lp_fallback', { message: 'Fallback a Long Polling.', code: event.code, reason: event.reason });
            stopClientPingInterval();
            startLongPollingInternal();
        };
    }

    function startLongPollingInternal() {
        if (longPollingIntervalId === null) {
            console.log('Starting Long Polling.');
            if (window.addMessageToHistory) window.addMessageToHistory('Avvio Long Polling...', 'info');
            
            longPollingIntervalId = setInterval(() => {
                if (messageQueue.length > 0 && !isProcessingQueue) {
                    const message = messageQueue.shift();
                    sendLongPollingMessageInternal(message);
                }
            }, 5000); // Poll interval
            startClientPingInterval(); // Also ping during LP
        }
    }

    function stopLongPollingInternal() {
        if (longPollingIntervalId !== null) {
            console.log('Stopping Long Polling.');
            if (window.addMessageToHistory) window.addMessageToHistory('Interruzione Long Polling.', 'info');
            clearInterval(longPollingIntervalId);
            longPollingIntervalId = null;
        }
    }

    function sendMessageInternal(message) {
        if (!message.request_id) { // Ensure request_id if not already set
            message.request_id = generateRequestIDInternal();
        }

        if (isWebSocketActive && ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify(message));
            if (window.addMessageToHistory && message.type !== 'ping') window.addMessageToHistory(`Messaggio inviato (WS): ${message.type} (ID: ${message.request_id})`, 'info');
        } else {
            console.log('WebSocket not available, queuing message for Long Polling:', message);
            messageQueue.push(message);
            if (window.addMessageToHistory && message.type !== 'ping') window.addMessageToHistory(`Messaggio accodato (LP): ${message.type} (ID: ${message.request_id})`, 'info');
            startLongPollingInternal();
        }
        return message.request_id;
    }

    function sendLongPollingMessageInternal(message) {
        isProcessingQueue = true;
        fetch(longPollingUrl, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(message),
        })
        .then(response => {
            if (!response.ok) throw new Error(`HTTP error: ${response.status}`);
            return response.json();
        })
        .then(data => {
            if (window.handleBackendMessage) {
                window.handleBackendMessage(data);
            }
        })
        .catch(error => {
            console.error('Error sending Long Polling message:', error);
            if (window.addMessageToHistory) window.addMessageToHistory(`Errore invio messaggio LP (ID: ${message.request_id}): ${error.message}`, 'error');
        })
        .finally(() => {
            isProcessingQueue = false;
        });
    }

    function processMessageQueueInternal() {
        if (isWebSocketActive && ws && ws.readyState === WebSocket.OPEN && messageQueue.length > 0 && !isProcessingQueue) {
            isProcessingQueue = true;
            console.log(`Processing message queue (${messageQueue.length} messages).`);
            if (window.addMessageToHistory) window.addMessageToHistory(`Elaborazione coda messaggi (${messageQueue.length})...`, 'info');
            
            while (messageQueue.length > 0) {
                const message = messageQueue.shift();
                ws.send(JSON.stringify(message));
                 if (window.addMessageToHistory && message.type !== 'ping') window.addMessageToHistory(`Messaggio da coda inviato (WS): ${message.type} (ID: ${message.request_id})`, 'info');
            }
            isProcessingQueue = false;
            console.log('Finished processing message queue.');
            if (window.addMessageToHistory) window.addMessageToHistory('Elaborazione coda messaggi completata.', 'info');
        }
    }

    function generateRequestIDInternal() {
        return Math.random().toString(36).substring(2, 15) + Math.random().toString(36).substring(2, 15);
    }

    function sendPingInternal() {
        const pingMessage = { type: 'ping', payload: Date.now() };
        const requestId = sendMessageInternal(pingMessage); // Uses the refactored sendMessageInternal
        pingTimestamps.set(requestId, Date.now());
    }

    function startClientPingInterval() {
        stopClientPingInterval(); // Clear existing before starting
        if (serverPingIntervalMs > 0) {
            console.log(`Starting client pings every ${serverPingIntervalMs}ms.`);
            clientPingIntervalId = setInterval(sendPingInternal, serverPingIntervalMs);
        } else {
            console.warn("Server ping interval is not positive, client pings not started.");
        }
    }

    function stopClientPingInterval() {
        if (clientPingIntervalId !== null) {
            console.log('Stopping client pings.');
            clearInterval(clientPingIntervalId);
            clientPingIntervalId = null;
        }
    }
    
    // Exposed global functions
    window.connectWebSocket = connectWebSocketInternal;
    window.sendMessage = sendMessageInternal;

    // Handle pong messages (called by app_logic.js)
    window.handlePongMessage = (message) => {
        const pongTimestamp = Date.now();
        const pingTimestamp = pingTimestamps.get(message.request_id);
        if (pingTimestamp) {
            const rtt = pongTimestamp - pingTimestamp;
            console.log(`Pong received for ID ${message.request_id}. RTT: ${rtt}ms.`);
            if (window.addMessageToHistory) window.addMessageToHistory(`Pong (ID: ${message.request_id}, RTT: ${rtt}ms)`, 'info');
            pingTimestamps.delete(message.request_id); // Clean up
        } else {
            console.warn(`Received pong for unknown request ID: ${message.request_id}`);
        }
    };

    // Handle config_update messages (called by app_logic.js)
    window.handleConfigUpdate = (message) => {
        if (message.payload && typeof message.payload.client_ping_interval_ms === 'number') {
            const newInterval = message.payload.client_ping_interval_ms;
            if (newInterval > 0 && newInterval !== serverPingIntervalMs) {
                console.log(`Server updated client_ping_interval_ms to: ${newInterval}`);
                if (window.addMessageToHistory) window.addMessageToHistory(`Intervallo Ping aggiornato dal server: ${newInterval}ms`, 'info');
                serverPingIntervalMs = newInterval;
                startClientPingInterval(); // Restart with new interval
            }
        }
    };
    console.log('websocket_service.js loaded');
})();
