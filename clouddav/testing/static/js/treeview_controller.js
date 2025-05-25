// static/js/treeview_controller.js
// Manages the treeview logic.

(function() {
    const treeviewRoot = document.getElementById('treeview-root');
    let selectedTreeviewElement = null;
    // Map to store the last request ID for a given directory path to avoid race conditions
    const lastRequestIds = new Map(); 

    function notifyAppLogic(message, type = 'info') {
        if (window.addMessageToHistory) {
            window.addMessageToHistory(`Treeview: ${message}`, type);
        }
        if (window.showToast) { // Assuming showToast is globally available from notification_service.js
            window.showToast(`Treeview: ${message}`, type);
        }
    }

    // Exposed to app_logic.js
    window.handleTreeviewBackendResponse = (message) => {
        console.log('TreeviewCtrl - Backend message received:', message);

        switch (message.type) {
            case 'get_filesystems_response':
                renderStorages(message.payload);
                notifyAppLogic('Elenco storage ricevuto.');
                break;
            case 'list_directory_response':
                const storageNameForResponse = message.payload?.storage_name || (message.payload?.items && message.payload.items.length > 0 ? message.payload.items[0].path.split('/')[0] : null); // Infer storage name if possible
                const dirPathForResponse = message.payload?.dir_path; // Path of the directory listed
                const requestIdForResponse = message.request_id;

                const mapKey = `${storageNameForResponse}:${dirPathForResponse}`;
                const expectedRequestId = lastRequestIds.get(mapKey);
                
                const requestingElement = document.querySelector(`li[data-request-id="${requestIdForResponse}"]`);

                if (!requestingElement) {
                    console.warn(`TreeviewCtrl - No requesting element found for request ID: ${requestIdForResponse}. Response might be obsolete.`);
                    return;
                }

                if (expectedRequestId && requestIdForResponse !== expectedRequestId) {
                    console.warn(`TreeviewCtrl - Obsolete list_directory_response for "${mapKey}" (Req ID: ${requestIdForResponse}, Expected: ${expectedRequestId}). Ignoring.`);
                    requestingElement.removeAttribute('data-request-id'); // Clean up attribute
                    return;
                }
                
                if (message.payload && Array.isArray(message.payload.items)) {
                    renderDirectoryContent(requestingElement, message.payload.items);
                    notifyAppLogic(`Contenuto directory "${requestingElement.dataset.path || '/'}" caricato.`);
                } else {
                    console.error('TreeviewCtrl - Invalid list_directory_response payload:', message.payload);
                    notifyAppLogic('Errore nel caricare contenuto directory: dati non validi.', 'error');
                }
                requestingElement.removeAttribute('data-request-id');
                lastRequestIds.delete(mapKey); // Clean up map entry
                break;
            case 'error':
                console.error('TreeviewCtrl - Error from backend:', message.payload.error);
                notifyAppLogic(`Errore dal backend: ${message.payload.error}`, 'error');
                // If an error occurred for a specific request, clean up its request ID attribute
                if (message.request_id) {
                    const erroredElement = document.querySelector(`li[data-request-id="${message.request_id}"]`);
                    if (erroredElement) {
                        erroredElement.removeAttribute('data-request-id');
                        const errStorage = erroredElement.dataset.storageName;
                        const errPath = erroredElement.dataset.path;
                        lastRequestIds.delete(`${errStorage}:${errPath}`);
                    }
                }
                break;
        }
    };

    // Exposed to app_logic.js
    window.requestInitialTreeviewData = () => {
        notifyAppLogic('Richiesta elenco storage...');
        if (window.sendMessage) { // sendMessage is from websocket_service.js
            window.sendMessage({ type: 'get_filesystems' });
        } else {
            console.error('TreeviewCtrl - sendMessage function is not available.');
            notifyAppLogic('Errore: Funzione sendMessage non disponibile.', 'error');
        }
    };

    function renderStorages(storages) {
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
        
        if (window.handleTreeviewSelect) { // Function in app_logic.js
            window.handleTreeviewSelect(storageName, itemPath, storageType);
        }

        if (clickedElement.classList.contains('directory')) {
            toggleDirectory(clickedElement);
        }
    }

    function toggleDirectory(directoryElement) {
        directoryElement.classList.toggle('open');
        const ul = directoryElement.querySelector('ul');

        if (directoryElement.classList.contains('open') && ul && ul.children.length === 0 && !directoryElement.dataset.requestId) {
            const storageName = directoryElement.dataset.storageName;
            const itemPath = directoryElement.dataset.path;
            
            notifyAppLogic(`Richiesta contenuto directory per "${itemPath || storageName || '/'}"...`);
            if (window.sendMessage) {
                const requestID = window.sendMessage({
                    type: 'list_directory',
                    payload: {
                        storage_name: storageName,
                        dir_path: itemPath,
                        page: 1,
                        items_per_page: 1000, // Request more items for treeview, pagination not typical here
                        name_filter: '',
                        timestamp_filter: ''
                    }
                });
                directoryElement.dataset.requestId = requestID;
                lastRequestIds.set(`${storageName}:${itemPath}`, requestID); // Store the latest request ID for this path
            } else {
                 console.error('TreeviewCtrl - sendMessage function is not available for list_directory.');
                 notifyAppLogic('Errore: Funzione sendMessage non disponibile.', 'error');
            }
        }
    }

    function renderDirectoryContent(directoryElement, items) {
        const ul = directoryElement.querySelector('ul');
        if (!ul) return;
        ul.innerHTML = ''; 

        const currentPath = directoryElement.dataset.path;
        const storageName = directoryElement.dataset.storageName;
        const storageType = directoryElement.dataset.storageType;

        if (currentPath !== '') {
            const parentLi = document.createElement('li');
            parentLi.classList.add('directory');
            parentLi.textContent = '..';
            parentLi.dataset.storageName = storageName;
            parentLi.dataset.path = currentPath.substring(0, currentPath.lastIndexOf('/'));
            parentLi.dataset.storageType = storageType;
            parentLi.addEventListener('click', handleTreeviewItemClick);
            ul.appendChild(parentLi);
        }

        items.sort((a, b) => {
            if (a.is_dir === b.is_dir) return a.name.localeCompare(b.name);
            return a.is_dir ? -1 : 1;
        });

        items.forEach(item => {
            if (!item.is_dir) return; // Only show directories in treeview

            const li = document.createElement('li');
            li.textContent = item.name;
            li.dataset.storageName = storageName;
            li.dataset.path = item.path;
            li.dataset.storageType = storageType;
            li.classList.add('directory');
            
            const childUl = document.createElement('ul');
            li.appendChild(childUl);
            li.addEventListener('click', handleTreeviewItemClick);
            ul.appendChild(li);
        });
    }

    // Global controls
    window.expandAllTreeviewNodes = () => {
        notifyAppLogic('Espansione di tutti i nodi del treeview...');
        treeviewRoot.querySelectorAll('li.directory').forEach(dirEl => {
            if (!dirEl.classList.contains('open')) {
                // Simulate a click to trigger loading if necessary
                dirEl.click(); // This will call handleTreeviewItemClick, then toggleDirectory
            }
        });
    };

    window.collapseAllTreeviewNodes = () => {
        notifyAppLogic('Compressione di tutti i nodi del treeview...');
        treeviewRoot.querySelectorAll('li.directory.open').forEach(dirEl => {
            dirEl.classList.remove('open');
        });
    };
    console.log('treeview_controller.js loaded');
})();
