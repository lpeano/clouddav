// websocket.js
// Gestisce la connessione WebSocket e il fallback su Long Polling.

// Attacca le funzioni principali all'oggetto window in modo che possano essere chiamate dalla pagina principale
// Attach main functions to the window object so they can be called from the main page
window.connectWebSocket = connectWebSocket;
window.sendMessage = sendMessage;
// handleBackendMessage will be defined by the main page and called by this script

let ws;
// Variabile per la connessione WebSocket
let isWebSocket = false; // Flag per indicare se la connessione corrente è WebSocket
let messageQueue = [];
// Coda per i messaggi da inviare quando la connessione non è disponibile
let isProcessingQueue = false;
// Flag per indicare se la coda dei messaggi è in fase de elaborazione
let longPollingInterval = null;
// Variabile per l'intervallo del Long Polling
let longPollingUrl = '/lp';
// Endpoint per Long Polling sul backend

// Determina il protocollo WebSocket in base al protocollo della pagina corrente
// Determine the WebSocket protocol based on the current page's protocol
const websocketProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
const websocketUrl = `${websocketProtocol}//${window.location.host}/ws`; // Endpoint per WebSocket sul backend

// Map to store timestamps of sent ping messages for RTT calculation
const pingTimestamps = new Map();

// Variabile per memorizzare l'intervallo de ping ricevuto dal server (in ms)
// Variable to store the ping interval received from the server (in ms)
let serverPingInterval = 10000; // Default value (10 seconds)

// Variabile per l'intervallo del ping periodico (sarà impostato da serverPingInterval)
// Variable for the periodic ping interval (will be set by serverPingInterval)
let pingInterval = null;


// Function to notify the main page about the connection status
// Funzione per notificare lo stato della connessione alla pagina principale
function notifyConnectionStatus(status, details = {}) {
    if (window.parent) {
        window.parent.postMessage({
            type: 'connection_status',
            payload: { status: status, details: details }
        }, '*');
    }

    // Aggiungi la chiamata a updateWebSocketStatusUI
    let displayMessage = '';
    switch (status) {
        case 'ws_connecting':
            displayMessage = 'Connessione in corso...';
            break;
        case 'ws_established':
            displayMessage = 'Connessione WebSocket attiva';
            break;
        case 'lp_fallback':
            displayMessage = 'WebSocket non disponibile (Long Polling)';
            break;
        case 'ws_error':
            displayMessage = `Errore WebSocket: ${details.error || 'generico'}`;
            break;
        default:
            displayMessage = `Stato sconosciuto: ${status}`;
    }
    updateWebSocketStatusUI(status, displayMessage);
}

// Funzione per notificare la pagina principale per aggiungere un messaggio alla cronologia
function notifyParentMessage(message, type = 'info') {
     if (window.parent && window.parent.addMessageToHistory) {
         window.parent.addMessageToHistory(`WebSocket/LP: ${message}`, type);
     } else {
         console.warn('WebSocket/LP - window.parent.addMessageToHistory non disponibile.');
     }
}


// Function to establish WebSocket connection
// Funzione per stabilire la connessione WebSocket
function connectWebSocket() {
    // Remove detailed logs to make the process quieter
    // console.log('Attempting WebSocket connection to:', websocketUrl);
    // Removed Log
    // Notify the main page that we are attempting WebSocket connection
    notifyConnectionStatus('ws_connecting'); // Questo chiamerà updateWebSocketStatusUI
    // Crea una nuova istanza WebSocket usando l'URL determinato dinamicamente
    // Crea una nuova istanza WebSocket usando l'URL determinato dinamicamente
    ws = new WebSocket(websocketUrl);
    // Handler for WebSocket connection open event
    // Gestore per l'evento di apertura della connessione WebSocket
    ws.onopen = () => {
        console.log('WebSocket connection established.');
        // *** INSERISCI QUI:
        notifyConnectionStatus('ws_established'); // Questo chiamerà updateWebSocketStatusUI
        // FINE INSERIMENTO ***
        isWebSocket = true; // Set the flag to true
        // Empty the pending message queue that were queued during connection absence
        // Svuota la coda dei messaggi in sospeso che sono stati accodati durante l'assenza de connessione
        processMessageQueue(); // [cite: 997]
        // Stop Long Polling if it was active, as we now have a WebSocket connection
        // Ferma il Long Polling se era attivo, poiché ora abbiamo una connessione WebSocket
        stopLongPolling(); // [cite: 998]
        // Start sending periodic pings - This will now use the serverPingInterval
         // Avvia l'invio di ping periodici - Questo userà ora serverPingInterval
        startPingInterval(); // [cite: 999]
    };

    // Handler for receiving a message via WebSocket
    // Gestore per l'evento de ricevere un messaggio via WebSocket
    ws.onmessage = (event) => {
        // console.log('WebSocket message received:', event.data);
        // Removed Log to make it quiet
        try {
            // Parsifica il messaggio JSON ricevuto
            const message = JSON.parse(event.data);
            // Chiama la funzione handleBackendMessage definita nella pagina principale
            // Chiama la funzione handleBackendMessage definita nella pagina principale
            if (window.handleBackendMessage) {
                 window.handleBackendMessage(message);
            } else {
                 console.warn('handleBackendMessage not defined in main page.');
                 notifyParentMessage('Avviso: handleBackendMessage non definita nel parent.', 'warning');
            }
        } catch (e) {
            console.error('Error parsing WebSocket message:', e);
            // Keep logs for debugging
            notifyParentMessage(`Errore nel parsing messaggio WebSocket: ${e.message}`, 'error');
        }
    };
    // Handler for WebSocket connection error event
    // Gestore per l'evento di errore della connessione WebSocket
    ws.onerror = (error) => {
        console.error('WebSocket error:', error);
        // Keep logs for internal debugging
        // Notify the main page that a WebSocket error occurred
        notifyConnectionStatus('ws_error', { error: error.message });
         notifyParentMessage(`Errore WebSocket: ${error.message}`, 'error');
        // The onclose event will handle the fallback in case of an error leading to closure
        // L'evento onclose gestirà il fallback in caso di errore che porta alla chiusura
    };
    // Handler for WebSocket connection close event
    // Gestore per l'evento di chiusura della connessione WebSocket
    ws.onclose = (event) => {
        console.log('WebSocket connection closed:', event.code, event.reason);
        // Keep logs for internal debugging
        isWebSocket = false;
        // Set the flag to false
        // Attempt fallback to Long Polling in case of connection closure
        // console.log('Attempting fallback to Long Polling...');
        // Removed Log to make it quiet
        // Notify the main page that we are switching to Long Polling
        notifyConnectionStatus('lp_fallback', { code: event.code, reason: event.reason });
         notifyParentMessage(`Connessione WebSocket chiusa (Codice: ${event.code}, Motivo: ${event.reason}). Tentativo Long Polling...`, 'warning');
        stopPingInterval(); // Stop sending pings when WS closes
        startLongPolling(); // Start Long Polling
    };
}

// Function to start Long Polling
function startLongPolling() {
    // Start Long Polling only if it's not already active
    if (longPollingInterval === null) {
        console.log('Starting Long Polling.'); // Keep this log
         notifyParentMessage('Avvio Long Polling...');

        // Send an initial ping message immediately when LP starts
        // Invia un messaggio de ping iniziale immediatamente quando LP si avvia
        // sendPing(); // We will rely on the periodic ping interval now

        // Set an interval to send messages from the queue (simulates regular polling)
        // In a real Long Polling, the client sends a request and the server keeps it open waiting for data.
        // This is a simplified approach where the client "polla" regularly by sending messages from its queue.
        // Imposta un intervallo per inviare messaggi dalla coda (simula il polling regolare)
        // In un vero Long Polling, il client invia una richiesta e il server la tiene aperta in attesa de dati.
        // Questo è un approccio semplificato dove il client "polla" regolarmente inviando messaggi dalla sua coda.
        longPollingInterval = setInterval(() => {
            // If there are messages in the queue and we are not already processing the queue
            // Se ci sono messaggi nella coda e non stiamo già elaborando la coda
            if (messageQueue.length > 0 && !isProcessingQueue) {
                 // Take the first message from the queue and remove it
                 // Prende il primo messaggio dalla coda e lo rimuove
                 const message = messageQueue.shift();
                 // Send the message via Long Polling
                 // Invia il messaggio tramite Long Polling
                 sendLongPollingMessage(message);
            } else if (!isProcessingQueue) {
                 // If the queue is empty and we are not processing, send a keep-alive message
                 // This helps to keep the logical connection active and detect network issues
                 // Se la coda è vuota e non stiamo elaborando, invia un messaggio de keep-alive
                 // Questo aiuta a mantenere la connessione logica attiva e a rilevare problemi de rete
                 // We already send periodic pings via startPingInterval, so we don't need a separate LP keep-alive here
                 // Inviamo già ping periodici tramite startPingInterval, quindi non abbiamo bisogno di un keep-alive LP separato qui
                 // sendLongPollingMessage({ type: 'ping', payload: 'LP alive' });
            }
        }, 5000); // Poll every 5 seconds (this value can be adjusted) - This interval is for processing the queue, not for sending pings

        // Start sending periodic pings also for Long Polling
        startPingInterval();
    }
}

// Function to stop Long Polling
function stopLongPolling() {
    // Stop the Long Polling interval if it's active
    if (longPollingInterval !== null) {
        console.log('Stopping Long Polling.'); // Keep this log
         notifyParentMessage('Interruzione Long Polling.');
        clearInterval(longPollingInterval);
        // Clear the interval
        longPollingInterval = null;
        // Reset the variable
    }
}


// Function to send a message via WebSocket or Long Polling
// Funzione per inviare un messaggio tramite WebSocket o Long Polling
function sendMessage(message) {
    // Add a unique ID to the request to correlate requests and responses
    // Aggiungi un ID univoco alla richiesta per correlare richieste e risposte
    message.request_id = generateRequestID();
    // Check if the WebSocket connection is open and available
    // Controlla se la connessione WebSocket è aperta e disponibile
    if (isWebSocket && ws && ws.readyState === WebSocket.OPEN) {
        // console.log('Sending message via WebSocket:', message);
        // Removed Log to make it quiet
        // Send the message as a JSON string via WebSocket
        // Invia il messaggio come stringa JSON via WebSocket
        ws.send(JSON.stringify(message));
         notifyParentMessage(`Messaggio inviato via WebSocket (ID: ${message.request_id}): ${message.type}`);
    } else {
        // If WebSocket is not available, queue the message to send via Long Polling
        // Se WebSocket non è disponibile, accoda il messaggio per inviarlo via Long Polling
        console.log('WebSocket not available, queuing message for Long Polling:', message); // Keep this log
        messageQueue.push(message);
        // Add the message to the queue
        // Start or continue Long Polling to empty the queue
        // Avvia o continua il Long Polling per svuotare la coda
        startLongPolling(); // Ensure LP is running to process the queue
         notifyParentMessage(`WebSocket non disponibile, messaggio accodato per LP (ID: ${message.request_id}): ${message.type}`);
    }
    return message.request_id; // Return the request ID
}

// Function to send a single message via Long Polling (POST request)
function sendLongPollingMessage(message) {
    // Segna che stiamo elaborando un messaggio dalla coda (to avoid multiple simultaneous sends)
    isProcessingQueue = true;
    // Use the Fetch API to send a POST request to the Long Polling endpoint
    // Usa l'API Fetch per inviare una richiesta POST all'endpoint Long Polling
    fetch(longPollingUrl, {
        method: 'POST', // HTTP POST method
        headers: {
            'Content-Type': 'application/json', // Indicates that the request body is JSON
        },
        body: JSON.stringify(message), // Request body: the JSON message
    })
    // Gestisce la risposta dalla richiesta Fetch
    .then(response => {
        // Controlla se la risposta HTTP è OK (status 2xx)
        if (!response.ok) {
            // Se la risposta non è OK, lancia un errore
            throw new Error(`HTTP error: ${response.status}`);
        }
        // Parsifica la risposta come JSON
        return response.json();
    })
    // Gestisce i dati JSON ricevuti nella risposta
    .then(data => {
        // console.log('Long Polling response received:', data); // Log removed to make it quiet
        // Call the handleBackendMessage function defined in the main page
        // Chiama la funzione handleBackendMessage definita nella pagina principale
        if (window.handleBackendMessage) {
             window.handleBackendMessage(data);
        } else {
             console.warn('handleBackendMessage not defined in main page.');
             notifyParentMessage('Avviso: handleBackendMessage non definita nel parent (LP response).', 'warning');
        }
    })
    // Gestisce eventuali errori durante la richiesta Fetch o il parsing della risposta
    .catch(error => {
        console.error('Error sending Long Polling message:', error); // Keep logs for internal debugging
        // In case of error, the message might need to be retried or handled differently.
        // For now, we log it.
        // In caso de errore, il messaggio potrebbe dover essere ritentato o gestito diversamente.
        // Per ora, lo logghiamo.
         notifyParentMessage(`Errore invio messaggio LP (ID: ${message.request_id}): ${error.message}`, 'error');
    })
    // Finally block is executed regardless of request success or failure
    .finally(() => {
        // Mark that processing of the current message is finished
        // Segna che l'elaborazione del messaggio corrente è terminata
        isProcessingQueue = false;
        // If the queue is not empty, the Long Polling timer will send the next message at the next interval.
        // Se la coda non è vuota, il timer del Long Polling invierà il prossimo messaggio al prossimo intervallo.
    });
}


// Function to process messages in the queue (used primarily when the WebSocket connection reopens)
function processMessageQueue() {
    // Check if the WebSocket connection is open, if there are messages in the queue, and if we are not already processing
    // Controlla se la connessione WebSocket è aperta, se ci sono messaggi in coda e se non stiamo già elaborando
    if (isWebSocket && ws && ws.readyState === WebSocket.OPEN && messageQueue.length > 0 && !isProcessingQueue) {
        isProcessingQueue = true;
        // Mark that we are processing the queue

        console.log(`Processing message queue (${messageQueue.length} messages).`); // Add this log
         notifyParentMessage(`Elaborazione coda messaggi (${messageQueue.length} messaggi)...`);

        // Iterate through the queue as long as there are messages
        // Itera sulla coda finché ci sono messaggi
        while (messageQueue.length > 0) {
            const message = messageQueue.shift();
            // Take the first message from the queue and remove it
            // Prende il primo messaggio dalla coda e lo rimuove
            // console.log('Sending queued message via WebSocket:', message);
            // Removed Log to make it quiet
            // Send the message via WebSocket
            // Invia il messaggio via WebSocket
            ws.send(JSON.stringify(message));
             notifyParentMessage(`Messaggio in coda inviato via WebSocket (ID: ${message.request_id}): ${message.type}`);
            // You might want to add a small delay here between messages to avoid overwhelming the server
            // Potresti voler aggiungere un piccolo ritardo qui tra i messaggi per evitare di sovraccaricare il server
            // await new Promise(resolve => setTimeout(resolve, 50));
        }

        isProcessingQueue = false;
        // End queue processing
        console.log('Finished processing message queue.'); // Add this log
         notifyParentMessage('Elaborazione coda messaggi completata.');
    }
}

// Function to generate a unique request ID
// Used to correlate sent requests with received responses
// Funzione helper per generare un ID di richiesta univoco
// Usato per correlare le richieste inviate con le risposte ricevute
function generateRequestID() {
    // Generate a random string based on timestamp and random numbers
    // Genera una stringa casuale basata su timestamp e numeri casuali
    return Math.random().toString(36).substring(2, 15) + Math.random().toString(36).substring(2, 15);
}

// Function to send a ping message with a timestamp
function sendPing() {
    const pingMessage = {
        type: 'ping',
        payload: Date.now(), // Send current timestamp as payload
    };
    const requestId = sendMessage(pingMessage); // Use sendMessage to handle request ID
    pingTimestamps.set(requestId, Date.now()); // Store the timestamp with the request ID
    // console.log(`Sent ping with ID: ${requestId}`); // Optional: log sent ping
     // notifyParentMessage(`Ping inviato (ID: ${requestId})`); // Troppo verboso per la cronologia
}


// Function to start sending periodic ping messages
function startPingInterval() {
    // Clear any existing interval before starting a new one
    // Cancella qualsiasi intervallo esistente prima de avviarne uno nuovo
    stopPingInterval();

    // Use the serverPingInterval value
    // Usa il valore de serverPingInterval
    if (serverPingInterval > 0) {
        console.log(`Starting periodic pings every ${serverPingInterval}ms.`);
         // notifyParentMessage(`Avvio ping periodici ogni ${serverPingInterval}ms.`); // Troppo verboso
        pingInterval = setInterval(sendPing, serverPingInterval);
    } else {
        console.warn("Server ping interval is not positive, periodic pings not started.");
         notifyParentMessage("Avviso: Intervallo ping server non positivo, ping periodici non avviati.", 'warning');
    }
}

// Function to stop sending periodic ping messages
function stopPingInterval() {
    if (pingInterval !== null) {
        console.log('Stopping periodic pings.');
         // notifyParentMessage('Interruzione ping periodici.'); // Troppo verboso
        clearInterval(pingInterval);
        pingInterval = null;
    }
}


// Override the handleBackendMessage function from the main page to also handle pong messages and config updates
// (assuming the main page defines this function)
// We already defined window.handleBackendMessage in index.html, so this part is handled there.
// We just need to ensure this script calls the parent's handleBackendMessage.

// The original handleBackendMessage is now defined in index.html and calls into the iframes.
// This websocket.js script will call window.parent.handleBackendMessage(message) when a message is received.
// This ensures the message is first processed by the main page's handler (for logging, etc.)
// before being potentially forwarded to the iframe-specific handlers.

// No changes needed here, as the logic is now handled in index.html's handleBackendMessage.

// Don't initiate WebSocket connection here. It will be initiated by the main page
// Don't initiate WebSocket connection here. It will be initiated by the main page
// connectWebSocket(); // Removed auto-connect

// Add a listener for messages posted from the main page (index.html)
// This is an alternative/complementary mechanism for communication between iframes
// Aggiungi un listener per i messaggi postati dalla pagina principale (index.html)
// Questo è un meccanismo alternativo/complementare per la comunicazione tra iframes
window.addEventListener('message', event => {
    // In produzione, verifica l'origine dell'evento per sicurezza: event.origin
    // console.log('Message received from iframe (websocket.js):', event.data); // Log removed

    // Handle specific messages from the main page if needed
    // Example: if the main page sends a message to force a reconnection
    // if (event.data.type === 'reconnect_websocket') {
    //     connectWebSocket();
    // }
});


const websocketStatusBox = document.getElementById('websocket-status-box');
const websocketStatusText = document.getElementById('websocket-status-text');

// Funzione per aggiornare l'interfaccia utente dello stato WebSocket
function updateWebSocketStatusUI(status, message) {
    if (!websocketStatusBox || !websocketStatusText) {
        console.warn('UI elements for WebSocket status not found.');
        return;
    }

    websocketStatusText.textContent = message;
    websocketStatusBox.classList.remove('status-green', 'status-red', 'status-yellow');

    switch (status) {
        case 'ws_established':
            websocketStatusBox.classList.add('status-green');
            break;
        case 'lp_fallback':
            websocketStatusBox.classList.add('status-red');
            break;
        case 'ws_connecting':
            websocketStatusBox.classList.add('status-yellow');
            break;
        case 'ws_error':
            websocketStatusBox.classList.add('status-red');
            break;
        // Aggiungi altri stati se necessario
    }
}

// Modifica la funzione notifyConnectionStatus per chiamare updateWebSocketStatusUI
function notifyConnectionStatus(status, details = {}) {
    if (window.parent) {
        window.parent.postMessage({
            type: 'connection_status',
            payload: { status: status, details: details }
        }, '*');
    }

    // Aggiungi la chiamata a updateWebSocketStatusUI
    let displayMessage = '';
    switch (status) {
        case 'ws_connecting':
            displayMessage = 'Connessione in corso...';
            break;
        case 'ws_established':
            displayMessage = 'Connessione WebSocket attiva';
            break;
        case 'lp_fallback':
            displayMessage = 'WebSocket non disponibile (Long Polling)';
            break;
        case 'ws_error':
            displayMessage = `Errore WebSocket: ${details.error || 'generico'}`;
            break;
        default:
            displayMessage = `Stato sconosciuto: ${status}`;
    }
    updateWebSocketStatusUI(status, displayMessage);
}
