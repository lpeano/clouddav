// static/js/app_logic.js

// Assicurati che window.config sia definito (da config.js caricato prima).
if (!window.config) {
    console.error("AppLogic FATAL: window.config non definito! Assicurati che config.js sia caricato prima di app_logic.js.");
    window.config = { 
        LogLevelDebug: 'DEBUG', LogLevelInfo: 'INFO', LogLevelWarning: 'WARNING', LogLevelError: 'ERROR',
        currentLogLevel: 'DEBUG', 
        IsLogLevel: function(levelToCheck) {
            if (!this.currentLogLevel) return false;
            const levels = { [this.LogLevelDebug]: 1, [this.LogLevelInfo]: 2, [this.LogLevelWarning]: 3, [this.LogLevelError]: 4 };
            const current = levels[this.currentLogLevel];
            const toCheck = levels[levelToCheck];
            if (current === undefined || toCheck === undefined) return false; 
            return toCheck >= current;
        }
    };
}

if (window.config && typeof config.IsLogLevel === 'function' && config.IsLogLevel(config.LogLevelInfo)) {
    console.info("--- EXECUTING app_logic.js ---");
}

function initializeStaticUI() {
    if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
        console.info("AppLogic - Initializing Static UI components (es. event listeners per bottoni non-WS, modali)...");
    }

    const msgHistoryHeader = document.getElementById('message-history-header');
    const msgHistoryArea = document.getElementById('message-history-area');
    const msgHistoryToggle = document.getElementById('message-history-toggle');
    if (msgHistoryHeader && msgHistoryArea && msgHistoryToggle) {
        msgHistoryHeader.addEventListener('click', () => {
            msgHistoryArea.classList.toggle('expanded');
            msgHistoryToggle.textContent = msgHistoryArea.classList.contains('expanded') ? '▼' : '▲';
        });
    } else {
        if (window.config && config.IsLogLevel(config.LogLevelWarning)) {
            console.warn("AppLogic: Elementi della message history non trovati nel DOM.");
        }
    }
    
    const uploadBoxHeader = document.getElementById('upload-progress-header');
    const uploadBox = document.getElementById('upload-progress-box');
    const uploadBoxToggle = document.getElementById('upload-progress-toggle');
    if (uploadBoxHeader && uploadBox && uploadBoxToggle) {
        const toggleUploadBoxInternal = () => { 
            const isExpanded = uploadBox.classList.toggle('expanded');
            uploadBoxToggle.textContent = isExpanded ? '▼' : '▲';
            if (window.checkOverallUploadStatus) window.checkOverallUploadStatus();
        };
        uploadBoxHeader.addEventListener('click', toggleUploadBoxInternal);
    } else {
         if (window.config && config.IsLogLevel(config.LogLevelWarning)) {
            console.warn("AppLogic: Elementi del box di progresso upload non trovati.");
        }
    }
    
    const expandBtn = document.querySelector('#global-controls button[onclick="expandAllTreeviewNodes()"]');
    if (expandBtn && window.expandAllTreeviewNodes) expandBtn.onclick = window.expandAllTreeviewNodes;
    else if(expandBtn && !window.expandAllTreeviewNodes && window.config && config.IsLogLevel(config.LogLevelWarning)) {
        console.warn("AppLogic: Bottone expandAllTreeviewNodes trovato ma funzione window.expandAllTreeviewNodes non definita.");
    }
    
    const collapseBtn = document.querySelector('#global-controls button[onclick="collapseAllTreeviewNodes()"]');
    if (collapseBtn && window.collapseAllTreeviewNodes) collapseBtn.onclick = window.collapseAllTreeviewNodes;
     else if(collapseBtn && !window.collapseAllTreeviewNodes && window.config && config.IsLogLevel(config.LogLevelWarning)) {
        console.warn("AppLogic: Bottone collapseAllTreeviewNodes trovato ma funzione window.collapseAllTreeviewNodes non definita.");
    }

    if (window.setupModalEventListeners && typeof window.setupModalEventListeners === 'function') { 
        window.setupModalEventListeners();
    } else if (window.config && config.IsLogLevel(config.LogLevelDebug)) {
        console.debug("AppLogic: window.setupModalEventListeners non definito o non è una funzione.");
    }
}

function initializeWebSocketDependentServices() {
    if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
        console.info("AppLogic - Initializing WebSocket dependent services...");
    }

    if (window.websocket_service && typeof window.websocket_service.connect === 'function') {
        if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
            console.info("AppLogic: Chiamata a window.websocket_service.connect()");
        }
        window.websocket_service.connect(); 
    } else {
        console.error("AppLogic - ERRORE CRITICO: window.websocket_service.connect non è una funzione.");
        if(window.showToast) window.showToast("Errore critico: Impossibile inizializzare la comunicazione con il server.", "error", 10000);
        return; 
    }

    if (window.requestInitialTreeviewData && typeof window.requestInitialTreeviewData === 'function') {
        if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
            console.info("AppLogic: Chiamata a window.requestInitialTreeviewData() per caricare gli storages iniziali.");
        }
        window.requestInitialTreeviewData(); 
    } else {
        console.error("AppLogic - ERRORE: window.requestInitialTreeviewData non definita o non è una funzione.");
    }
}

function attemptCoreServicesInitialization() {
    if (window.websocket_service_ready_flag === true) { 
        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
            console.info("AppLogic: WebSocket service era già pronto (flag=true). Inizializzazione dei servizi dipendenti da WebSocket.");
        }
        initializeWebSocketDependentServices();
    } else {
        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
            console.info("AppLogic: In attesa dell'evento 'websocketServiceReady' (flag non ancora true).");
        }
        window.addEventListener('websocketServiceReady', function onWebsocketReady() {
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                console.info("AppLogic: Evento 'websocketServiceReady' ricevuto. Inizializzazione dei servizi dipendenti da WebSocket.");
            }
            initializeWebSocketDependentServices();
        }, { once: true });
    }
}

document.addEventListener('DOMContentLoaded', () => {
    if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
        console.info("AppLogic: DOMContentLoaded - Inizializzazione UI statica.");
    }
    initializeStaticUI(); 
    attemptCoreServicesInitialization(); 
});

window.app_logic = {
    initializeAppUI: () => { 
        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelWarning)) {
            console.warn("AppLogic: initializeAppUI chiamata direttamente. L'inizializzazione è ora gestita da DOMContentLoaded e attemptCoreServicesInitialization.");
        }
    },
    
    updateWebSocketStatusOnMainPage: (statusType, message) => {
        // Log INCONDIZIONATO per vedere se la funzione viene chiamata
        console.log(`[[AppLogic DEBUG]] updateWebSocketStatusOnMainPage chiamata con statusType='${statusType}', message='${message}'`);
        
        const statusBox = document.getElementById('websocket-status-box');
        const statusText = document.getElementById('websocket-status-text');
        
        if (statusBox && statusText) {
            console.log(`[[AppLogic DEBUG]] Elementi statusBox e statusText TROVATI. Testo attuale: '${statusText.textContent}'`);
            statusText.textContent = message || 'N/A';
            statusBox.className = ''; // Rimuovi tutte le classi di stato precedenti
            statusBox.classList.add('status-' + statusType.replace('ws_', '')); 
            console.log(`[[AppLogic DEBUG]] statusText.textContent impostato a '${statusText.textContent}'. statusBox.className impostato a '${statusBox.className}'`);
        } else {
            console.warn("[[AppLogic DEBUG]] Elementi UI per lo stato WebSocket non trovati (#websocket-status-box, #websocket-status-text) durante updateWebSocketStatusOnMainPage.");
        }
    },

    addMessageToHistory: (message, type = 'info') => {
        const messageList = document.getElementById('message-list');
        if (!messageList) {
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelWarning)) {
                console.warn("AppLogic: Elemento #message-list non trovato per addMessageToHistory.");
            }
            return;
        }

        const li = document.createElement('li');
        const timestampSpan = document.createElement('span');
        timestampSpan.className = 'message-timestamp';
        timestampSpan.textContent = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
        
        li.appendChild(timestampSpan);
        li.appendChild(document.createTextNode(" " + message)); 
        li.className = `log-entry log-${type}`; 

        messageList.prepend(li);
        const maxMessages = 50; 
        while (messageList.children.length > maxMessages) {
            messageList.removeChild(messageList.lastChild);
        }
    },
    
    handleBackendMessage: (message) => {
        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
            console.info("AppLogic: Ricevuto messaggio generico dal backend:", message);
        }
        if (message.type === 'cache_invalidate_event' && message.payload) {
            const { storage_name, dir_path, items_per_page, only_directories } = message.payload;
            if (window.dataService && window.dataService.invalidateCacheForPath) {
                 if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                    console.info(`AppLogic: Ricevuto evento di invalidazione cache per ${storage_name}:${dir_path}`);
                }
                window.dataService.invalidateCacheForPath(storage_name, dir_path, items_per_page, only_directories);
            }
        }
    },
    
    handleConfigUpdate: (payload) => {
        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
            console.info("AppLogic: Ricevuto config_update dal server:", payload);
        }
        if (payload && typeof payload.client_ping_interval_ms === 'number') {
            if (window.config) { 
                window.config.clientPingIntervalMs = payload.client_ping_interval_ms;
                 if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                    console.info(`AppLogic: client_ping_interval_ms aggiornato a ${payload.client_ping_interval_ms}ms dalla configurazione del server.`);
                }
            }
        }
    }
};

if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
    console.info("app_logic.js caricato e window.app_logic definito.");
}
