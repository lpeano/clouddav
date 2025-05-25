// static/js/app_logic.js
// Main application logic, UI interactions, and coordination

// --- Global State (esempio) ---
let currentSelectedStorageName = '';
let currentSelectedDirPath = '';
let filesToUploadGlobally = []; // Per passare i file alla modale chunk
let globalSelectedChunkSize = 4 * 1024 * 1024; // Default 4MB
let globalSelectedParallelChunks = 4; // Default 4
const globalUploadItems = new Map();
let globalItemToDelete = null;
let globalOngoingDeleteCheck = null; // Per memorizzare i dettagli durante il check del contenuto della dir

// --- DOM Element Cache ---
const messageHistoryArea = document.getElementById('message-history-area');
const messageHistoryHeader = document.getElementById('message-history-header');
const messageHistoryToggle = document.getElementById('message-history-toggle');
const messageList = document.getElementById('message-list');

const uploadProgressBox = document.getElementById('upload-progress-box');
const uploadProgressHeader = document.getElementById('upload-progress-header');
const uploadHeaderText = document.getElementById('upload-header-text');
const uploadProgressToggle = document.getElementById('upload-progress-toggle');
const uploadItemsContainer = document.getElementById('upload-items-container');

const chunkSizeModal = document.getElementById('chunk-size-modal');
const chunkSizeSlider = document.getElementById('chunk-size-slider');
const chunkSizeDisplay = document.getElementById('chunk-size-display');
const parallelChunksSlider = document.getElementById('parallel-chunks-slider');
const parallelChunksDisplay = document.getElementById('parallel-chunks-display');
const startUploadBtn = document.getElementById('start-upload-btn');
const cancelModalBtn = document.getElementById('cancel-modal-btn');

const createFolderBtn = document.getElementById('create-folder-btn');
const createFolderModal = document.getElementById('create-folder-modal');
const newFolderNameInput = document.getElementById('new-folder-name');
const cancelCreateFolderBtn = document.getElementById('cancel-create-folder-btn');
const confirmCreateFolderBtn = document.getElementById('confirm-create-folder-btn');

const deleteConfirmModal = document.getElementById('delete-confirm-modal');
const deleteItemNameElement = document.getElementById('delete-item-name');
const deleteWarningMessageElement = document.getElementById('delete-warning-message');
const cancelDeleteBtn = document.getElementById('cancel-delete-btn');
const confirmDeleteBtn = document.getElementById('confirm-delete-btn');

const filelistLoadingOverlay = document.getElementById('filelist-loading-overlay');
const websocketStatusBox = document.getElementById('websocket-status-box');
const websocketStatusText = document.getElementById('websocket-status-text');


// --- Initialization ---
function initializeAppUI() {
    console.log('AppLogic - Initializing App UI...');
    addMessageToHistory('Applicazione inizializzata.');

    // Event Listeners for UI elements
    if (messageHistoryHeader) messageHistoryHeader.addEventListener('click', toggleMessageHistory);
    if (uploadProgressHeader) uploadProgressHeader.addEventListener('click', toggleUploadProgressBox);
    if (uploadProgressToggle) uploadProgressToggle.addEventListener('click', toggleUploadProgressBox);

    if (chunkSizeSlider) chunkSizeSlider.addEventListener('input', (event) => {
        if(chunkSizeDisplay) chunkSizeDisplay.textContent = formatBytesForDisplay(parseInt(event.target.value, 10));
    });
    if (parallelChunksSlider) parallelChunksSlider.addEventListener('input', (event) => {
        if(parallelChunksDisplay) parallelChunksDisplay.textContent = parseInt(event.target.value, 10);
    });
    if (startUploadBtn) startUploadBtn.addEventListener('click', handleStartUploadConfirm);
    if (cancelModalBtn) cancelModalBtn.addEventListener('click', hideChunkSizeModal);
    if (chunkSizeModal) chunkSizeModal.addEventListener('click', (event) => {
        if (event.target === chunkSizeModal) hideChunkSizeModal();
    });

    if (createFolderBtn) createFolderBtn.addEventListener('click', () => showCreateFolderModal());
    if (confirmCreateFolderBtn) confirmCreateFolderBtn.addEventListener('click', handleCreateFolderConfirm);
    if (cancelCreateFolderBtn) cancelCreateFolderBtn.addEventListener('click', hideCreateFolderModal);
    if (createFolderModal) createFolderModal.addEventListener('click', (event) => {
        if (event.target === createFolderModal) hideCreateFolderModal();
    });

    if (confirmDeleteBtn) confirmDeleteBtn.addEventListener('click', handleDeleteItemConfirm);
    if (cancelDeleteBtn) cancelDeleteBtn.addEventListener('click', hideDeleteConfirmModal);
    if (deleteConfirmModal) deleteConfirmModal.addEventListener('click', (event) => {
        if (event.target === deleteConfirmModal) hideDeleteConfirmModal();
    });

    // Initialize WebSocket connection
    if (window.connectWebSocket) {
        window.connectWebSocket();
    } else {
        console.error('AppLogic - connectWebSocket function is not available.');
        addMessageToHistory('Errore: Funzione di connessione WebSocket non disponibile.', 'error');
    }

    // Request initial treeview data
    if (window.requestInitialTreeviewData) {
        window.requestInitialTreeviewData();
    } else {
        console.error('AppLogic - requestInitialTreeviewData function is not available.');
    }
    console.log('AppLogic - App UI Initialized.');
}

// --- Message History ---
function addMessageToHistory(message, type = 'info') {
    if (!messageList) return;
    const li = document.createElement('li');
    const timestampSpan = document.createElement('span');
    timestampSpan.className = 'message-timestamp';
    timestampSpan.textContent = new Date().toLocaleTimeString();
    li.appendChild(timestampSpan);
    li.appendChild(document.createTextNode(message));
    messageList.prepend(li);
    const maxMessages = 50;
    while (messageList.children.length > maxMessages) {
        messageList.removeChild(messageList.lastChild);
    }
}
window.addMessageToHistory = addMessageToHistory; // Make global for other modules

function toggleMessageHistory() {
    if (!messageHistoryArea || !messageHistoryToggle) return;
    messageHistoryArea.classList.toggle('expanded');
    messageHistoryToggle.textContent = messageHistoryArea.classList.contains('expanded') ? '▼' : '▲';
}

// --- Upload Progress ---
function showUploadProgressBox() {
    if (!uploadProgressBox || !uploadProgressHeader || !uploadProgressToggle || !uploadHeaderText) return;
    uploadProgressBox.style.display = 'block';
    if (!uploadProgressBox.classList.contains('expanded') && !uploadProgressBox.classList.contains('upload-final-state')) {
        uploadProgressBox.classList.add('expanded');
        uploadProgressToggle.textContent = '▼';
    }
    uploadHeaderText.textContent = 'Upload in corso...';
    uploadProgressHeader.removeEventListener('click', hideUploadProgressBox); // Ensure no double listeners
    uploadProgressToggle.removeEventListener('click', hideUploadProgressBox);
    uploadProgressHeader.addEventListener('click', toggleUploadProgressBox);
    uploadProgressToggle.addEventListener('click', toggleUploadProgressBox);
}

function hideUploadProgressBox() {
    if (!uploadProgressBox || !uploadItemsContainer || !uploadProgressHeader || !uploadProgressToggle || !uploadHeaderText) return;
    uploadProgressBox.style.display = 'none';
    uploadItemsContainer.innerHTML = '';
    globalUploadItems.clear();
    uploadProgressBox.classList.remove('expanded', 'upload-final-state');
    uploadHeaderText.textContent = 'Upload';
    uploadProgressToggle.textContent = '▲';
    uploadProgressHeader.removeEventListener('click', hideUploadProgressBox);
    uploadProgressToggle.removeEventListener('click', hideUploadProgressBox);
    uploadProgressHeader.addEventListener('click', toggleUploadProgressBox); // Re-add toggle listener
    uploadProgressToggle.addEventListener('click', toggleUploadProgressBox);
}

function toggleUploadProgressBox() {
    if (!uploadProgressBox || !uploadProgressToggle || !uploadHeaderText) return;
    if (!uploadProgressBox.classList.contains('upload-final-state')) {
        uploadProgressBox.classList.toggle('expanded');
        uploadProgressToggle.textContent = uploadProgressBox.classList.contains('expanded') ? '▼' : '▲';
        uploadHeaderText.textContent = uploadProgressBox.classList.contains('expanded') ? 'Upload in corso...' : 'Upload';
    } else {
        hideUploadProgressBox();
    }
}

function updateUploadProgressUI(uploadId, fileName, percentage, statusText, filePath, isFinal = false, statusClass = '') {
    if (!uploadItemsContainer) return;
    let uploadItemData = globalUploadItems.get(uploadId);
    let uploadItemElement = uploadItemData ? uploadItemData.element : null;

    if (!uploadItemElement) {
        uploadItemElement = document.createElement('div');
        uploadItemElement.className = 'upload-item';
        uploadItemElement.dataset.uploadId = uploadId;
        uploadItemElement.innerHTML = `
            <div class="upload-item-header">
                <div class="upload-file-name"></div>
                <button class="upload-cancel-button">✕</button>
            </div>
            <div class="upload-progress-bar-container" title="">
                <div class="upload-progress-bar">0%</div>
            </div>
            <div class="upload-status-text"></div>`;
        uploadItemsContainer.appendChild(uploadItemElement);
        globalUploadItems.set(uploadId, { element: uploadItemElement, fileName, percentage, statusText, filePath });

        uploadItemElement.querySelector('.upload-cancel-button').addEventListener('click', () => {
            if (window.confirm(`Sei sicuro di voler annullare l'upload di "${fileName}"?`)) {
                if (window.cancelUploadFile) {
                    window.cancelUploadFile(uploadId);
                }
            }
        });
    } else {
         globalUploadItems.get(uploadId).percentage = percentage;
         globalUploadItems.get(uploadId).statusText = statusText;
         globalUploadItems.get(uploadId).filePath = filePath;
    }

    uploadItemElement.querySelector('.upload-file-name').textContent = fileName;
    const progressBar = uploadItemElement.querySelector('.upload-progress-bar');
    progressBar.style.width = `${percentage}%`;
    progressBar.textContent = `${percentage.toFixed(0)}%`;
    uploadItemElement.querySelector('.upload-status-text').textContent = statusText;
    uploadItemElement.querySelector('.upload-progress-bar-container').title = filePath;

    uploadItemElement.classList.remove('complete', 'failed', 'cancelled', 'upload-final-state');
    if (isFinal) {
        uploadItemElement.classList.add('upload-final-state', statusClass);
        uploadItemElement.querySelector('.upload-cancel-button').style.display = 'none';
    } else {
        uploadItemElement.querySelector('.upload-cancel-button').style.display = 'block';
    }
    checkOverallUploadStatus();
    if (!uploadProgressBox || uploadProgressBox.style.display === 'none') {
        showUploadProgressBox();
    }
}
window.updateGlobalUploadProgress = updateUploadProgressUI;


function checkOverallUploadStatus() {
    if (!uploadProgressBox || !uploadProgressToggle || !uploadHeaderText || !uploadProgressHeader) return;
    let allFinal = true;
    let anyFailed = false;
    if (globalUploadItems.size === 0) {
        hideUploadProgressBox();
        return;
    }
    globalUploadItems.forEach(item => {
        if (!item.element.classList.contains('upload-final-state')) allFinal = false;
        if (item.element.classList.contains('failed')) anyFailed = true;
    });

    if (allFinal) {
        uploadProgressBox.classList.add('upload-final-state');
        uploadProgressToggle.textContent = '✕';
        uploadHeaderText.textContent = anyFailed ? 'Upload(s) Fallito/i' : 'Upload(s) Completato/i';
        uploadProgressHeader.removeEventListener('click', toggleUploadProgressBox);
        uploadProgressToggle.removeEventListener('click', toggleUploadProgressBox);
        uploadProgressHeader.addEventListener('click', hideUploadProgressBox);
        uploadProgressToggle.addEventListener('click', hideUploadProgressBox);
    } else {
        uploadProgressBox.classList.remove('upload-final-state');
        uploadProgressToggle.textContent = uploadProgressBox.classList.contains('expanded') ? '▼' : '▲';
        uploadHeaderText.textContent = 'Upload in corso...';
        uploadProgressHeader.removeEventListener('click', hideUploadProgressBox);
        uploadProgressToggle.removeEventListener('click', hideUploadProgressBox);
        uploadProgressHeader.addEventListener('click', toggleUploadProgressBox);
        uploadProgressToggle.addEventListener('click', toggleUploadProgressBox);
    }
}


// --- Modals ---
function formatBytesForDisplay(bytes) {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(i === 0 ? 0 : 1))} ${sizes[i]}`;
}

function showChunkSizeModal(files) {
    if(!chunkSizeModal || !chunkSizeSlider || !chunkSizeDisplay || !parallelChunksSlider || !parallelChunksDisplay) return;
    filesToUploadGlobally = files;
    const firstFileSize = files.length > 0 ? files[0].size : 1024 * 1024 * 10;
    chunkSizeSlider.max = firstFileSize > 0 ? firstFileSize : 1024 * 1024 * 10;
    chunkSizeSlider.value = Math.min(globalSelectedChunkSize, parseInt(chunkSizeSlider.max,10) );
    chunkSizeDisplay.textContent = formatBytesForDisplay(parseInt(chunkSizeSlider.value, 10));
    parallelChunksSlider.value = globalSelectedParallelChunks;
    parallelChunksDisplay.textContent = globalSelectedParallelChunks;
    chunkSizeModal.style.display = 'flex';
}
window.showGlobalChunkSizeModal = showChunkSizeModal;

function hideChunkSizeModal() {
    if(chunkSizeModal) chunkSizeModal.style.display = 'none';
}

function handleStartUploadConfirm() {
    if(!chunkSizeSlider || !parallelChunksSlider) return;
    globalSelectedChunkSize = parseInt(chunkSizeSlider.value, 10);
    globalSelectedParallelChunks = parseInt(parallelChunksSlider.value, 10);
    hideChunkSizeModal();
    if (window.initiateFileUploads) {
        window.initiateFileUploads(filesToUploadGlobally, globalSelectedChunkSize, globalSelectedParallelChunks);
    }
    filesToUploadGlobally = [];
}

function showCreateFolderModal() {
    if(!createFolderModal || !newFolderNameInput) return;
    newFolderNameInput.value = '';
    createFolderModal.style.display = 'flex';
    newFolderNameInput.focus();
}
window.showGlobalCreateFolderModal = showCreateFolderModal;

function hideCreateFolderModal() {
    if(createFolderModal) createFolderModal.style.display = 'none';
}

function handleCreateFolderConfirm() {
    if(!newFolderNameInput) return;
    const folderName = newFolderNameInput.value.trim();
    if (folderName) {
        hideCreateFolderModal();
        if (window.requestCreateDirectory) {
            window.requestCreateDirectory(currentSelectedStorageName, currentSelectedDirPath, folderName);
        }
    } else {
        if (window.showToast) window.showToast('Il nome della cartella non può essere vuoto.', 'warning');
    }
}

function showDeleteConfirmModal(itemDetails) {
    if(!deleteConfirmModal || !deleteItemNameElement || !deleteWarningMessageElement) return;
    globalItemToDelete = itemDetails;
    deleteItemNameElement.textContent = `Elemento: "${itemDetails.itemName}"`;
    deleteWarningMessageElement.textContent = itemDetails.warningMessage || "Sei sicuro di voler eliminare questo elemento? Questa azione non può essere annullata.";
    deleteConfirmModal.style.display = 'flex';
}
window.showGlobalDeleteConfirmModal = showDeleteConfirmModal;

function hideDeleteConfirmModal() {
    if(deleteConfirmModal) deleteConfirmModal.style.display = 'none';
    globalItemToDelete = null;
}

function handleDeleteItemConfirm() {
    if (globalItemToDelete) {
        const itemToProcess = globalItemToDelete; // Copia prima di nascondere e nullare
        hideDeleteConfirmModal();
        if (window.requestDeleteItem) {
            window.requestDeleteItem(itemToProcess.storageName, itemToProcess.itemPath, itemToProcess.itemName);
        }
    }
}

// --- Loading Spinner (Filelist specific) ---
function showFilelistLoadingSpinner() {
    if (filelistLoadingOverlay) filelistLoadingOverlay.style.display = 'flex';
}
window.showFilelistLoadingSpinner = showFilelistLoadingSpinner;

function hideFilelistLoadingSpinner() {
    if (filelistLoadingOverlay) filelistLoadingOverlay.style.display = 'none';
}
window.hideFilelistLoadingSpinner = hideFilelistLoadingSpinner;


// --- WebSocket Status UI ---
function updateWebSocketStatusUI(status, message) {
    if (!websocketStatusBox || !websocketStatusText) {
        console.warn('AppLogic - UI elements for WebSocket status not found.');
        return;
    }
    websocketStatusText.textContent = message;
    websocketStatusBox.classList.remove('status-green', 'status-red', 'status-yellow', 'status-grey'); // Rimuovi classi specifiche e default
    switch (status) {
        case 'ws_established':
            websocketStatusBox.classList.add('status-green');
            break;
        case 'lp_fallback':
        case 'ws_error':
            websocketStatusBox.classList.add('status-red');
            break;
        case 'ws_connecting':
            websocketStatusBox.classList.add('status-yellow');
            break;
        default:
            websocketStatusBox.classList.add('status-grey');
            break;
    }
}
window.updateWebSocketStatusOnMainPage = updateWebSocketStatusUI;


// --- Main Communication Handlers ---
function handleTreeviewSelect(storageName, itemPath, storageType) {
    console.log(`AppLogic - Treeview selection: Storage=${storageName}, Path=${itemPath || 'root'}, Type=${storageType}`);
    addMessageToHistory(`Navigato a: ${storageName}${itemPath ? '/' + itemPath : '/'}`);
    currentSelectedStorageName = storageName;
    currentSelectedDirPath = itemPath;

    if(createFolderBtn) {
        if (storageType === 'local' || storageType === 'azure-blob') {
            createFolderBtn.style.display = 'inline-block';
            createFolderBtn.disabled = false;
        } else {
            createFolderBtn.style.display = 'none';
        }
    }

    if (window.loadFilelistForPath) {
        window.loadFilelistForPath(storageName, itemPath);
    }
}
window.handleTreeviewSelect = handleTreeviewSelect;

// Centralized backend message handler
window.handleBackendMessage = (message) => {
    console.log('AppLogic - Backend message received:', message); // Questa riga corrisponde a app_logic.js:404
    if (window.addMessageToHistory) {
        addMessageToHistory(`Backend: ${message.type} (ID: ${message.request_id || 'N/A'})`);
    }

    if (message.type === 'list_directory_response') {
        if (window.handleTreeviewBackendResponse) {
            window.handleTreeviewBackendResponse(message);
        }
        if (window.handleFilelistBackendResponse) {
            window.handleFilelistBackendResponse(message);
        }
    } else if (message.type === 'get_filesystems_response') {
        if (window.handleTreeviewBackendResponse) {
            window.handleTreeviewBackendResponse(message);
        }
    } else if (message.type === 'create_directory_response' ||
               message.type === 'delete_item_response' ||
               message.type === 'check_directory_contents_request_response') { // <<< MODIFICA QUI
        if (window.handleFilelistBackendResponse) {
            window.handleFilelistBackendResponse(message);
        }
    } else if (message.type === 'pong') {
        if (window.handlePongMessage) {
            window.handlePongMessage(message);
        }
    } else if (message.type === 'config_update') {
        if (window.handleConfigUpdate) {
             window.handleConfigUpdate(message);
        }
    } else if (message.type === 'error') {
        console.error('AppLogic - Backend error:', message.payload ? message.payload.error : 'Errore sconosciuto');
        if (window.showToast) {
            window.showToast(`Errore dal Backend: ${message.payload ? message.payload.error : 'Errore sconosciuto'}`, 'error');
        }
        if (window.handleTreeviewBackendResponse) {
            window.handleTreeviewBackendResponse(message);
        }
        if (window.handleFilelistBackendResponse) {
            window.handleFilelistBackendResponse(message);
        }

        let isErrorHandledByController = false;
        if (message.request_id) {
            if (document.querySelector(`li[data-request-id="${message.request_id}"]`)) {
                // Treeview might handle its spinner
            }
            if (window.lastFilelistRequestId === message.request_id) {
                isErrorHandledByController = true;
            }
        }
        if (!isErrorHandledByController && window.hideFilelistLoadingSpinner) {
            window.hideFilelistLoadingSpinner();
        }
    } else {
        // QUESTA RIGA (o simile) corrisponde a app_logic.js:472 nel tuo log se il tipo non è gestito
        console.warn(`AppLogic - Unhandled backend message type: ${message.type}`, message);
    }
};

// --- Load Event ---
window.addEventListener('load', initializeAppUI);

console.log('app_logic.js loaded');
