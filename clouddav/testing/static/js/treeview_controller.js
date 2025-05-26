// static/js/treeview_controller.js
// Manages the treeview logic.

(function() {
    const treeviewRoot = document.getElementById('treeview-root');
    let selectedTreeviewElement = null;
    const lastRequestIds = new Map(); // Map<pathKey, requestID>

    function notifyAppLogic(message, type = 'info') {
        if (window.addMessageToHistory) {
            window.addMessageToHistory(`Treeview: ${message}`, type);
        }
        if (window.showToast && (type === 'error' || type === 'warning')) {
            window.showToast(`Treeview: ${message}`, type);
        }
    }

    window.handleTreeviewBackendResponse = (message) => {
        console.log('TreeviewCtrl - Backend message received:', message);
        const { type, payload, request_id: messageRequestId } = message;

        if (type === 'get_filesystems_response') {
            if (payload && Array.isArray(payload)) {
                renderStorages(payload);
                notifyAppLogic('Elenco storage ricevuto.');
            } else {
                console.error('TreeviewCtrl - Invalid get_filesystems_response payload:', payload);
                notifyAppLogic('Errore nel caricare elenco storage: dati non validi.', 'error');
            }
            return;
        }

        if (!messageRequestId) {
            console.warn(`TreeviewCtrl - Message type ${type} received without request_id. Ignoring.`);
            return;
        }

        let pathKeyForThisResponse = null;
        let responseStorageName = null;
        let responseDirPath = null;
        let targetElementForErrorCleanup = null;

        if (type === 'list_directory_response') {
            if (payload && typeof payload.storage_name === 'string' && typeof payload.dir_path === 'string') {
                responseStorageName = payload.storage_name;
                responseDirPath = payload.dir_path; // Può essere stringa vuota per la root
                pathKeyForThisResponse = `${responseStorageName}:${responseDirPath}`;
            } else {
                console.error(`TreeviewCtrl - list_directory_response (ID: ${messageRequestId}) MISSING storage_name or dir_path in payload. Payload received:`, JSON.stringify(payload));
                lastRequestIds.forEach((reqId, pk) => {
                    if (reqId === messageRequestId) {
                        pathKeyForThisResponse = pk;
                        const parts = pk.split(':');
                        responseStorageName = parts[0];
                        responseDirPath = parts.slice(1).join(':');
                        console.warn(`TreeviewCtrl - Fallback: Found pathKey \"${pathKeyForThisResponse}\" for ID ${messageRequestId} by searching map.`);
                    }
                });
                if (!pathKeyForThisResponse) {
                    notifyAppLogic(`Risposta directory (ID: ${messageRequestId}) ricevuta senza informazioni di percorso sufficienti. Impossibile elaborare.`, 'error');
                    return;
                }
            }
        } else if (type === 'error') {
            lastRequestIds.forEach((reqId, pk) => {
                if (reqId === messageRequestId) {
                    pathKeyForThisResponse = pk;
                    const parts = pk.split(':');
                    responseStorageName = parts[0];
                    responseDirPath = parts.slice(1).join(':');
                }
            });
            if (!pathKeyForThisResponse) {
                console.warn(`TreeviewCtrl - Error message (ID: ${messageRequestId}) received, but no pending request found for this ID in lastRequestIds. Error:`, payload ? payload.error : "Unknown error");
                const orphanedElement = document.querySelector(`li[data-pending-request-id=\"${messageRequestId}\"]`);
                if (orphanedElement) orphanedElement.removeAttribute('data-pending-request-id');
                return;
            }
        } else {
            console.warn(`TreeviewCtrl - Unhandled message type ${type} with request_id ${messageRequestId}. Ignoring.`);
            return;
        }

        const latestExpectedRequestIdForPath = lastRequestIds.get(pathKeyForThisResponse);

        if (latestExpectedRequestIdForPath !== messageRequestId) {
            console.warn(`TreeviewCtrl - Response for ID ${messageRequestId} (path: ${pathKeyForThisResponse}) is NOT the latest expected ID (${latestExpectedRequestIdForPath || 'none'}) for this path. Ignoring as obsolete.`);
            return;
        }

        const targetElement = document.querySelector(`li[data-storage-name=\"${responseStorageName}\"][data-path=\"${responseDirPath}\"]`);

        if (!targetElement) {
            console.warn(`TreeviewCtrl - Target DOM element for path ${pathKeyForThisResponse} not found, though response ID ${messageRequestId} was expected. Tree might have been re-rendered. Cleaning up request ID from map.`);
            lastRequestIds.delete(pathKeyForThisResponse);
            return;
        }
        targetElementForErrorCleanup = targetElement;

        if (type === 'list_directory_response') {
            if (payload && Array.isArray(payload.items)) {
                renderDirectoryContent(targetElement, payload.items);
                notifyAppLogic(`Contenuto directory \"${responseDirPath || responseStorageName || '/'}\" caricato.`);
            } else {
                console.error('TreeviewCtrl - Invalid list_directory_response payload items (after pathKey check):', payload);
                notifyAppLogic('Errore nel caricare contenuto directory: dati non validi.', 'error');
                targetElement.classList.remove('open');
                const ul = targetElement.querySelector('ul');
                if (ul) ul.innerHTML = '';
            }
        } else if (type === 'error') {
            console.error(`TreeviewCtrl - Error from backend for path ${pathKeyForThisResponse} (Req ID: ${messageRequestId}):`, payload.error);
            notifyAppLogic(`Errore caricamento directory ${pathKeyForThisResponse}: ${payload.error}`, 'error');
            if (targetElementForErrorCleanup) {
                targetElementForErrorCleanup.classList.remove('open');
                const ul = targetElementForErrorCleanup.querySelector('ul');
                if (ul) ul.innerHTML = '';
            }
        }

        lastRequestIds.delete(pathKeyForThisResponse);
    };

    window.requestInitialTreeviewData = () => {
        notifyAppLogic('Richiesta elenco storage...');
        if (window.sendMessage) {
            window.sendMessage({ type: 'get_filesystems' });
        } else {
            console.error('TreeviewCtrl - sendMessage function is not available.');
            notifyAppLogic('Errore: Funzione sendMessage non disponibile.', 'error');
        }
    };

    function renderStorages(storages) {
        if (!treeviewRoot) {
            console.error("TreeviewCtrl - treeviewRoot element not found in DOM.");
            return;
        }
        treeviewRoot.innerHTML = '';
        if (!Array.isArray(storages)) {
            console.error('TreeviewCtrl - storages argument is not an array:', storages);
            notifyAppLogic('Errore nel rendering storage: dati non validi.', 'error');
            return;
        }
        storages.forEach(storageCfg => {
            const li = document.createElement('li');
            li.classList.add('directory');
            li.textContent = storageCfg.name;
            li.dataset.storageName = storageCfg.name;
            li.dataset.path = '';
            li.dataset.storageType = storageCfg.type;
            li.addEventListener('click', handleTreeviewItemClick);

            const ul = document.createElement('ul');
            li.appendChild(ul);
            treeviewRoot.appendChild(li);
        });
    }

    function handleTreeviewItemClick(event) {
        event.stopPropagation();
        const clickedElement = event.currentTarget;

        if (selectedTreeviewElement) {
            selectedTreeviewElement.classList.remove('selected');
        }
        clickedElement.classList.add('selected');
        selectedTreeviewElement = clickedElement;

        const storageName = clickedElement.dataset.storageName;
        const itemPath = clickedElement.dataset.path;
        const storageType = clickedElement.dataset.storageType;
        const isDirectory = clickedElement.classList.contains('directory'); // Controlla se è una directory

        // Notifica sempre la selezione, sia per file che per directory
        if (window.handleTreeviewSelect) {
            window.handleTreeviewSelect(storageName, itemPath, storageType);
        }

        // Espandi/comprimi solo se è una directory
        if (isDirectory) {
            if (event.target === clickedElement || clickedElement.contains(event.target)) {
                 toggleDirectory(clickedElement);
            }
        }
    }

    function toggleDirectory(directoryElement) {
        const isOpen = directoryElement.classList.contains('open');
        const ul = directoryElement.querySelector('ul');

        if (!ul) {
            console.error("TreeviewCtrl - Elemento ul mancante per la directory:", directoryElement);
            return;
        }

        if (isOpen) {
            directoryElement.classList.remove('open');
            // Non è necessario pulire ul.innerHTML qui,
            // perché le regole CSS si occuperanno di nascondere l'ul.
            return;
        }

        directoryElement.classList.add('open');

        let hasChildElements = false;
        for (let i = 0; i < ul.childNodes.length; i++) {
            if (ul.childNodes[i].nodeType === Node.ELEMENT_NODE) {
                hasChildElements = true;
                break;
            }
        }

        if (!hasChildElements) {
            const storageName = directoryElement.dataset.storageName;
            const itemPath = directoryElement.dataset.path;
            const pathKey = `${storageName}:${itemPath}`;

            if (lastRequestIds.has(pathKey)) {
                console.log(`TreeviewCtrl - Richiesta per il percorso ${pathKey} (ID: ${lastRequestIds.get(pathKey)}) è già considerata in corso. Ignorata nuova richiesta.`);
                return;
            }

            notifyAppLogic(`Richiesta contenuto directory per \"${itemPath || storageName || '/'}\"...`);
            if (window.sendMessage) {
                const requestID = window.sendMessage({
                    type: 'list_directory',
                    payload: {
                        storage_name: storageName,
                        dir_path: itemPath,
                        page: 1, // Per il treeview, probabilmente vorrai sempre la prima pagina
                        items_per_page: 1000, // Un numero grande per ottenere tutte le sottodirectory
                                          // Se il backend ora pagina correttamente solo le directory,
                                          // questo potrebbe essere rivisto.
                        name_filter: '',       // Solitamente non si filtra per nome nel treeview
                        timestamp_filter: '',  // Solitamente non si filtra per data nel treeview
                        only_directories: true // << MODIFICA: Richiedi solo directory
                    }
                });
                lastRequestIds.set(pathKey, requestID);
            } else {
                 console.error('TreeviewCtrl - sendMessage function is not available for list_directory.');
                 notifyAppLogic('Errore: Funzione sendMessage non disponibile.', 'error');
                 directoryElement.classList.remove('open');
            }
        }
    }

    function renderDirectoryContent(directoryElement, items) {
        const ul = directoryElement.querySelector('ul');
        if (!ul) {
            console.error("TreeviewCtrl - Cannot render content, ul not found for:", directoryElement);
            return;
        }
        ul.innerHTML = '';

        const storageName = directoryElement.dataset.storageName;
        const storageType = directoryElement.dataset.storageType;

        // Ordina solo per nome, dato che tutti gli items dovrebbero essere directory
        items.sort((a, b) => a.name.localeCompare(b.name));

        items.forEach(item => {
            // Se il backend invia solo directory, item.is_dir dovrebbe essere sempre true.
            // Aggiungiamo un controllo per sicurezza o se il backend potesse inviare misto in altri scenari.
            if (!item.is_dir) { // Non dovrebbe accadere se only_directories:true funziona
                console.warn(`TreeviewCtrl: Ricevuto un file (${item.name}) quando ci si aspettavano solo directory.`);
                return;
            }

            const li = document.createElement('li');
            li.textContent = item.name;
            li.dataset.storageName = storageName;
            li.dataset.path = item.path;
            li.dataset.storageType = storageType; // Potrebbe non essere più necessario se solo dir
            
            li.classList.add('directory'); // Tutti gli item sono directory
            const childUl = document.createElement('ul');
            li.appendChild(childUl);
            
            li.addEventListener('click', handleTreeviewItemClick);
            ul.appendChild(li);
        });
    }

    window.expandAllTreeviewNodes = () => {
        if (!treeviewRoot) return;
        notifyAppLogic('Espansione di tutti i nodi del treeview...');
        treeviewRoot.querySelectorAll('li.directory:not(.open)').forEach(dirEl => {
            // Simula un click per espandere e caricare il contenuto se necessario
            // Assicurati che il click non causi problemi se chiamato in rapida successione
            // o se l'elemento non è completamente inizializzato.
            // Una chiamata diretta a toggleDirectory potrebbe essere più sicura in alcuni casi,
            // ma il click simula più da vicino l'interazione utente.
            dirEl.click(); 
        });
    };

    window.collapseAllTreeviewNodes = () => {
        if (!treeviewRoot) return;
        notifyAppLogic('Compressione di tutti i nodi del treeview...');
        treeviewRoot.querySelectorAll('li.directory.open').forEach(dirEl => {
            dirEl.classList.remove('open');
            // Non è necessario pulire ul.innerHTML qui,
            // perché le regole CSS si occuperanno di nascondere l'ul.
        });
    };
    console.log('treeview_controller.js loaded');
})();
