// static/js/app_logic.js

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

let isWebsocketServiceReadyForApp = window.websocket_service_ready_flag || false;
let isDataServiceReadyForApp = window.data_service_ready_flag || false;
window.app_logic_ready_flag = false; 

function initializeStaticUI() {
    if (config.IsLogLevel(config.LogLevelInfo)) {
        console.info("AppLogic - Initializing Static UI components...");
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
        if (config.IsLogLevel(config.LogLevelWarning)) console.warn("AppLogic: Elementi della message history non trovati.");
    }
    
    const uploadBoxHeader = document.getElementById('upload-progress-header');
    const uploadBox = document.getElementById('upload-progress-box');
    const uploadBoxToggle = document.getElementById('upload-progress-toggle');
    if (uploadBoxHeader && uploadBox && uploadBoxToggle) {
        const toggleUploadBoxInternal = () => { 
            const isExpanded = uploadBox.classList.toggle('expanded');
            uploadBoxToggle.textContent = isExpanded ? '▼' : '▲';
            if (window.app_logic && typeof window.app_logic.checkOverallUploadStatus === 'function') window.app_logic.checkOverallUploadStatus();
        };
        uploadBoxHeader.addEventListener('click', toggleUploadBoxInternal);
    } else {
         if (config.IsLogLevel(config.LogLevelWarning)) console.warn("AppLogic: Elementi del box di progresso upload non trovati.");
    }
    
    const expandBtn = document.querySelector('#global-controls button[onclick="expandAllTreeviewNodes()"]');
    if (expandBtn && window.expandAllTreeviewNodes) expandBtn.onclick = window.expandAllTreeviewNodes;
    else if(expandBtn && !window.expandAllTreeviewNodes && config.IsLogLevel(config.LogLevelWarning)) console.warn("AppLogic: Bottone expandAll ma window.expandAllTreeviewNodes non def.");
    
    const collapseBtn = document.querySelector('#global-controls button[onclick="collapseAllTreeviewNodes()"]');
    if (collapseBtn && window.collapseAllTreeviewNodes) collapseBtn.onclick = window.collapseAllTreeviewNodes;
     else if(collapseBtn && !window.collapseAllTreeviewNodes && config.IsLogLevel(config.LogLevelWarning)) console.warn("AppLogic: Bottone collapseAll ma window.collapseAllTreeviewNodes non def.");

    // Chiama setupModalEventListeners che ora è un metodo di window.app_logic
    if (window.app_logic && typeof window.app_logic.setupModalEventListeners === 'function') { 
        window.app_logic.setupModalEventListeners();
    } else if (config.IsLogLevel(config.LogLevelDebug)) {
        console.debug("AppLogic: window.app_logic.setupModalEventListeners non definito o non è una funzione al momento di initializeStaticUI.");
    }
}

function initializeCoreServicesAndApp() { 
    if (config.IsLogLevel(config.LogLevelInfo)) {
        console.info("AppLogic - Initializing Core Services (WebSocket connection and initial data fetch)...");
    }

    if (window.websocket_service && typeof window.websocket_service.connect === 'function') {
        if (config.IsLogLevel(config.LogLevelInfo)) console.info("AppLogic: Chiamata a window.websocket_service.connect()");
        window.websocket_service.connect(); 
    } else {
        console.error("AppLogic - ERRORE CRITICO: window.websocket_service.connect non è una funzione.");
        if(window.showToast) window.showToast("Errore critico: Impossibile inizializzare la comunicazione.", "error", 10000);
        return; 
    }

    if (window.requestInitialTreeviewData && typeof window.requestInitialTreeviewData === 'function') {
        if (config.IsLogLevel(config.LogLevelInfo)) console.info("AppLogic: Chiamata a window.requestInitialTreeviewData() per caricare gli storages iniziali.");
        window.requestInitialTreeviewData(); 
    } else {
        console.error("AppLogic - ERRORE: window.requestInitialTreeviewData non definita o non è una funzione.");
    }

    window.app_logic_ready_flag = true;
    const appLogicReadyEvent = new CustomEvent('appLogicReady');
    window.dispatchEvent(appLogicReadyEvent);
    if (config.IsLogLevel(config.LogLevelInfo)) {
        console.info("AppLogic: Flag 'app_logic_ready_flag' impostato e evento 'appLogicReady' emesso.");
    }
}

function attemptCoreServicesInitialization() {
    if (isWebsocketServiceReadyForApp && isDataServiceReadyForApp) { 
        if (config.IsLogLevel(config.LogLevelInfo)) {
            console.info("AppLogic: Tutti i servizi (WebSocket & DataService) sono pronti. Inizializzazione dei servizi core e app.");
        }
        initializeCoreServicesAndApp(); 
    } else {
        if (config.IsLogLevel(config.LogLevelInfo)) {
            let waitingFor = [];
            if (!isWebsocketServiceReadyForApp) waitingFor.push("websocketServiceReady");
            if (!isDataServiceReadyForApp) waitingFor.push("dataServiceReady");
            console.info(`AppLogic: In attesa degli eventi: ${waitingFor.join(' e ')}.`);
        }
    }
}

document.addEventListener('DOMContentLoaded', () => {
    if (config.IsLogLevel(config.LogLevelInfo)) console.info("AppLogic: DOMContentLoaded - Inizializzazione UI statica.");
    initializeStaticUI(); 
    attemptCoreServicesInitialization(); 
});

window.addEventListener('websocketServiceReady', () => {
    if (config.IsLogLevel(config.LogLevelInfo)) console.info("AppLogic: Evento 'websocketServiceReady' ricevuto.");
    isWebsocketServiceReadyForApp = true;
    attemptCoreServicesInitialization(); 
}, { once: true });

window.addEventListener('dataServiceReady', () => {
    if (config.IsLogLevel(config.LogLevelInfo)) console.info("AppLogic: Evento 'dataServiceReady' ricevuto.");
    isDataServiceReadyForApp = true;
    attemptCoreServicesInitialization(); 
}, { once: true });


window.app_logic = {
    initializeAppUI: () => { 
        if (config.IsLogLevel(config.LogLevelWarning)) console.warn("AppLogic: initializeAppUI chiamata. L'init è gestito da DOMContentLoaded e eventi ready.");
    },
    
    updateWebSocketStatusOnMainPage: (statusType, message) => {
        console.log(`[[AppLogic DEBUG]] updateWebSocketStatusOnMainPage chiamata con statusType='${statusType}', message='${message}'`);
        const statusBox = document.getElementById('websocket-status-box');
        const statusText = document.getElementById('websocket-status-text');
        if (statusBox && statusText) {
            console.log(`[[AppLogic DEBUG]] Elementi statusBox e statusText TROVATI. Testo attuale: '${statusText.textContent}'`);
            statusText.textContent = message || 'N/A';
            statusBox.className = ''; 
            statusBox.classList.add('status-' + statusType.replace('ws_', '')); 
            console.log(`[[AppLogic DEBUG]] statusText.textContent impostato a '${statusText.textContent}'. statusBox.className impostato a '${statusBox.className}'`);
        } else {
            console.warn("[[AppLogic DEBUG]] Elementi UI per lo stato WebSocket non trovati durante updateWebSocketStatusOnMainPage.");
        }
    },

    addMessageToHistory: (message, type = 'info') => { 
        const messageList = document.getElementById('message-list');
        if (!messageList) { if (config.IsLogLevel(config.LogLevelWarning)) console.warn("AppLogic: Elemento #message-list non trovato."); return; }
        const li = document.createElement('li');
        const timestampSpan = document.createElement('span');
        timestampSpan.className = 'message-timestamp';
        timestampSpan.textContent = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
        li.appendChild(timestampSpan); li.appendChild(document.createTextNode(" " + message)); 
        li.className = `log-entry log-${type}`; 
        messageList.prepend(li);
        const maxMessages = 50; 
        while (messageList.children.length > maxMessages) messageList.removeChild(messageList.lastChild);
    },
    
    handleBackendMessage: (message) => { 
        if (config.IsLogLevel(config.LogLevelInfo)) console.info("AppLogic: Ricevuto messaggio generico dal backend:", message);
        if (message.type === 'cache_invalidate_event' && message.payload) {
            const { storage_name, dir_path, items_per_page, only_directories } = message.payload;
            if (window.dataService && window.dataService.invalidateCacheForPath) {
                 if (config.IsLogLevel(config.LogLevelInfo)) console.info(`AppLogic: Ricevuto evento di invalidazione cache per ${storage_name}:${dir_path}`);
                window.dataService.invalidateCacheForPath(storage_name, dir_path, items_per_page, only_directories);
            }
        }
    },
    
    handleConfigUpdate: (payload) => { 
        if (config.IsLogLevel(config.LogLevelInfo)) console.info("AppLogic: Ricevuto config_update dal server:", payload);
        if (payload && typeof payload.client_ping_interval_ms === 'number') {
            if (window.config) { 
                window.config.clientPingIntervalMs = payload.client_ping_interval_ms;
                 if (config.IsLogLevel(config.LogLevelInfo)) console.info(`AppLogic: client_ping_interval_ms aggiornato a ${payload.client_ping_interval_ms}ms.`);
            }
        }
    },
    setCurrentPathGlobal: (storageName, dirPath, storageType) => {
        if (config.IsLogLevel(config.LogLevelInfo)) {
            console.info(`AppLogic: setCurrentPathGlobal chiamato con ${storageName}:${dirPath || '/'} (Tipo: ${storageType})`);
        }
        const currentPathArea = document.getElementById('current-path-area');
        if (currentPathArea) {
            currentPathArea.textContent = `Percorso Corrente: ${storageName}${dirPath ? ':' + dirPath : ''}`;
        }
        const createFolderBtn = document.getElementById('create-folder-btn');
        if (createFolderBtn) {
            createFolderBtn.style.display = storageName ? 'inline-block' : 'none';
        }

        if (window.filelist_controller && typeof window.filelist_controller.handlePathChange === 'function') {
            window.filelist_controller.handlePathChange(storageName, dirPath, storageType);
        } else if (config.IsLogLevel(config.LogLevelWarning)) {
            console.warn("AppLogic: filelist_controller.handlePathChange non trovato per notificare cambio path.");
        }
    },

    // --- FUNZIONI DI UPLOAD E MODALI (STUB) ---
    openChunkSizeModalForUpload: (files, storageName, dirPath) => {
        if (config.IsLogLevel(config.LogLevelInfo)) {
            console.info("AppLogic: openChunkSizeModalForUpload chiamata con", {filesCount: files ? files.length : 0, storageName, dirPath});
        }
        const modal = document.getElementById('chunk-size-modal');
        const slider = document.getElementById('chunk-size-slider');
        const display = document.getElementById('chunk-size-display');
        const parallelSlider = document.getElementById('parallel-chunks-slider');
        const parallelDisplay = document.getElementById('parallel-chunks-display');
        const startBtn = document.getElementById('start-upload-btn');
        const cancelBtn = document.getElementById('cancel-modal-btn');

        if (!modal || !slider || !display || !parallelSlider || !parallelDisplay || !startBtn || !cancelBtn) {
            console.error("AppLogic: Elementi della modale dimensione chunk non trovati. Fallback a upload diretto.");
            if (typeof window.app_logic.handleFileUpload === 'function') {
                window.app_logic.handleFileUpload(files, storageName, dirPath, 4 * 1024 * 1024, 4); // Default
            } else {
                 console.error("AppLogic: handleFileUpload non definito per fallback.");
                 if(window.showToast) window.showToast("Errore: Funzione di upload principale mancante.", "error");
            }
            return;
        }
        
        // Logica per aggiornare il display dello slider (esempio)
        const updateChunkDisplay = () => {
            const bytes = parseInt(slider.value, 10);
            if (bytes < 1024) display.textContent = `${bytes} B`;
            else if (bytes < 1024 * 1024) display.textContent = `${(bytes / 1024).toFixed(1)} KB`;
            else display.textContent = `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
        };
        slider.oninput = updateChunkDisplay;
        updateChunkDisplay(); // Inizializza

        const updateParallelDisplay = () => parallelDisplay.textContent = parallelSlider.value;
        parallelSlider.oninput = updateParallelDisplay;
        updateParallelDisplay(); // Inizializza

        modal.style.display = 'flex';

        startBtn.onclick = () => {
            const chunkSize = parseInt(slider.value, 10);
            const parallelChunks = parseInt(parallelSlider.value, 10);
            modal.style.display = 'none';
            if (typeof window.app_logic.handleFileUpload === 'function') {
                window.app_logic.handleFileUpload(files, storageName, dirPath, chunkSize, parallelChunks);
            } else {
                console.error("AppLogic: handleFileUpload non definito dopo conferma modale.");
                if(window.showToast) window.showToast("Errore: Funzione di upload principale mancante.", "error");
            }
        };
        cancelBtn.onclick = () => modal.style.display = 'none';
    },

    handleFileUpload: (files, storageName, dirPath, chunkSize, parallelChunks) => {
        if (config.IsLogLevel(config.LogLevelInfo)) {
            console.info("AppLogic: handleFileUpload chiamata con:", { filesCount: files ? files.length : 0, storageName, dirPath, chunkSize, parallelChunks });
        }
        if (window.showToast) {
            window.showToast(`Upload per ${files ? files.length : 0} file/s non ancora implementato completamente.`, "warning");
        }
        console.warn("AppLogic: Logica di handleFileUpload non implementata completamente.");
        
        if (window.addUploadItemToUI && files) { 
            for (let i = 0; i < files.length; i++) {
                const file = files[i];
                const uploadId = `stub_upload_${Date.now()}_${i}`;
                window.addUploadItemToUI(uploadId, file.name, file.size);
                setTimeout(() => {
                    if(window.updateUploadProgressInUI) window.updateUploadProgressInUI(uploadId, 50, 'uploading');
                }, 1000);
                setTimeout(() => {
                    if(window.updateUploadProgressInUI) window.updateUploadProgressInUI(uploadId, 100, 'complete');
                    if(window.filelist_controller && window.filelist_controller.refreshCurrentView) {
                        // window.filelist_controller.refreshCurrentView(); 
                    }
                }, 2000 + (i * 300)); // Simula upload scaglionati
            }
        }
    },

    openCreateFolderModal: (storageName, dirPath) => {
        if (config.IsLogLevel(config.LogLevelInfo)) {
            console.info(`AppLogic: openCreateFolderModal chiamata per ${storageName}:${dirPath || '/'}`);
        }
        const modal = document.getElementById('create-folder-modal');
        const folderNameInput = document.getElementById('new-folder-name');
        const confirmBtn = document.getElementById('confirm-create-folder-btn');
        const cancelBtn = document.getElementById('cancel-create-folder-btn');

        if (!modal || !folderNameInput || !confirmBtn || !cancelBtn) {
            console.error("AppLogic: Elementi della modale 'Crea Cartella' non trovati.");
            if(window.showToast) window.showToast("Errore: Impossibile aprire la modale per creare una cartella.", "error");
            return;
        }
        folderNameInput.value = '';
        modal.style.display = 'flex';

        confirmBtn.onclick = () => {
            const newFolderName = folderNameInput.value.trim();
            if (!newFolderName) {
                if(window.showToast) window.showToast("Il nome della cartella non può essere vuoto.", "warning");
                return;
            }
            modal.style.display = 'none';
            if (config.IsLogLevel(config.LogLevelInfo)) {
                console.info(`AppLogic: Creazione cartella '${newFolderName}' in ${storageName}:${dirPath || '/'}`);
            }
            // QUI: Invia messaggio WebSocket 'create_directory' tramite dataService o websocket_service
            // Esempio:
            // window.dataService.createDirectory(storageName, (dirPath ? dirPath + '/' : '') + newFolderName)
            //  .then(() => { 
            //      notifyAppLogic(`Cartella '${newFolderName}' creata.`, "success");
            //      if(window.filelist_controller) window.filelist_controller.refreshCurrentView();
            //  })
            //  .catch(err => notifyAppLogic(`Errore creazione cartella: ${err.message || err.error}`, "error"));
            if(window.showToast) window.showToast(`Funzione 'Crea Cartella' (${newFolderName}) non ancora implementata.`, "info");
        };
        cancelBtn.onclick = () => modal.style.display = 'none';
    },

    confirmDeleteItem: (storageName, itemPath, itemName, isDir) => {
        if (config.IsLogLevel(config.LogLevelInfo)) {
            console.info(`AppLogic: confirmDeleteItem chiamata per ${storageName}:${itemPath} (Nome: ${itemName}, Directory: ${isDir})`);
        }
        const modal = document.getElementById('delete-confirm-modal');
        const itemNameSpan = document.getElementById('delete-item-name');
        const warningMsgSpan = document.getElementById('delete-warning-message'); // Opzionale
        const confirmBtn = document.getElementById('confirm-delete-btn');
        const cancelBtn = document.getElementById('cancel-delete-btn');

        if (!modal || !itemNameSpan || !confirmBtn || !cancelBtn) {
            console.error("AppLogic: Elementi della modale 'Conferma Eliminazione' non trovati.");
            if(window.showToast) window.showToast("Errore: Impossibile aprire la modale di conferma eliminazione.", "error");
            return;
        }
        
        itemNameSpan.textContent = `Elemento: ${itemName}`;
        if (isDir && warningMsgSpan) {
            warningMsgSpan.textContent = "Sei sicuro di voler eliminare questa cartella e TUTTO il suo contenuto? Questa azione non può essere annullata.";
        } else if (warningMsgSpan) {
            warningMsgSpan.textContent = "Sei sicuro di voler eliminare questo file? Questa azione non può essere annullata.";
        }
        
        modal.style.display = 'flex';

        confirmBtn.onclick = () => {
            modal.style.display = 'none';
            if (config.IsLogLevel(config.LogLevelInfo)) {
                console.info(`AppLogic: Eliminazione confermata per ${storageName}:${itemPath}`);
            }
            // QUI: Invia messaggio WebSocket 'delete_item' tramite dataService o websocket_service
            // Esempio:
            // window.dataService.deleteItem(storageName, itemPath)
            //  .then(() => { 
            //      notifyAppLogic(`Elemento '${itemName}' eliminato.`, "success");
            //      if(window.filelist_controller) window.filelist_controller.refreshCurrentView();
            //  })
            //  .catch(err => notifyAppLogic(`Errore eliminazione: ${err.message || err.error}`, "error"));
            if(window.showToast) window.showToast(`Funzione 'Elimina Elemento' (${itemName}) non ancora implementata.`, "info");
        };
        cancelBtn.onclick = () => modal.style.display = 'none';
    },

    setupModalEventListeners: () => {
        if (config.IsLogLevel(config.LogLevelInfo)) {
            console.info("AppLogic: Chiamata a setupModalEventListeners.");
        }
        // Questo è un buon posto per aggiungere event listener generici per chiudere le modali
        // cliccando sull'overlay, o con il tasto ESC, se desiderato.
        // Esempio per la modale di chunk size (da adattare per le altre):
        const chunkSizeModal = document.getElementById('chunk-size-modal');
        if (chunkSizeModal) {
            chunkSizeModal.addEventListener('click', (event) => {
                if (event.target === chunkSizeModal) { // Se si clicca sull'overlay stesso
                    chunkSizeModal.style.display = 'none';
                }
            });
        }
        // Aggiungi logica simile per 'create-folder-modal' e 'delete-confirm-modal'
        const createFolderModal = document.getElementById('create-folder-modal');
        if (createFolderModal) {
            createFolderModal.addEventListener('click', (event) => {
                if (event.target === createFolderModal) createFolderModal.style.display = 'none';
            });
        }
        const deleteConfirmModal = document.getElementById('delete-confirm-modal');
        if (deleteConfirmModal) {
            deleteConfirmModal.addEventListener('click', (event) => {
                if (event.target === deleteConfirmModal) deleteConfirmModal.style.display = 'none';
            });
        }
    }
};

if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
    console.info("app_logic.js caricato e window.app_logic definito.");
}
// L'evento 'appLogicReady' viene emesso da initializeCoreServicesAndApp
