// Si assume che window.config sia definito globalmente (es. da config.js)
// e che window.dataService e window.app_logic siano disponibili.

// --------------- treeview_controller.js ---------------
(() => {
    const treeviewRoot = document.getElementById('treeview-root');
    let selectedTreeviewElement = null;
    let isDataServiceReady = window.data_service_ready_flag || false; 

    function notifyAppLogic(message, type = 'info') {
        if (window.app_logic && window.app_logic.addMessageToHistory) {
            window.app_logic.addMessageToHistory(`Treeview: ${message}`, type);
        }
        if (window.showToast && (type === 'error' || type === 'warning')) {
            window.showToast(`Treeview: ${message}`, type);
        }
    }
    
    if (!isDataServiceReady) {
        window.addEventListener('dataServiceReady', () => {
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                console.info("TreeviewCtrl: Evento 'dataServiceReady' ricevuto.");
            }
            isDataServiceReady = true;
            // Se requestInitialTreeviewData è stata chiamata prima che dataService fosse pronto,
            // e la chiamata è fallita, potresti volerla ritentare qui.
            // Tuttavia, app_logic.js dovrebbe orchestrare la chiamata iniziale a requestInitialTreeviewData
            // solo dopo che TUTTI i servizi necessari sono pronti.
        }, { once: true });
    }

    window.requestInitialTreeviewData = async function() { 
        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
            console.info("TreeviewCtrl: Chiamata a requestInitialTreeviewData.");
        }

        if (!isDataServiceReady || !window.dataService || typeof window.dataService.fetchFileSystems !== 'function') {
            const errorMsg = "TreeviewCtrl - ERRORE CRITICO: dataService o dataService.fetchFileSystems non disponibile.";
            console.error(errorMsg);
            notifyAppLogic("Errore: Servizio dati non pronto per caricare gli storage.", "error");
            
            if (!isDataServiceReady && window.config && config.IsLogLevel(config.LogLevelInfo)) {
                console.info("TreeviewCtrl: dataService non ancora pronto. requestInitialTreeviewData attenderà l'evento 'dataServiceReady' (se app_logic lo chiama di nuovo).");
            }
            return; // Non procedere se il servizio non è pronto
        }

        try {
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                console.debug("TreeviewCtrl: Chiamata a dataService.fetchFileSystems()");
            }
            const storagesPayload = await window.dataService.fetchFileSystems(); 
            if (storagesPayload && Array.isArray(storagesPayload)) { 
                renderStorages(storagesPayload);
                notifyAppLogic("Elenco storage caricato.");
            } else {
                console.error("TreeviewCtrl: Payload da dataService.fetchFileSystems non è un array:", storagesPayload);
                notifyAppLogic("Errore formato dati storages da dataService.", "error");
            }
        } catch (errorPayload) {
            console.error("TreeviewCtrl: Errore da dataService.fetchFileSystems:", errorPayload);
            notifyAppLogic(`Errore caricamento storages: ${errorPayload.message || (errorPayload.error || 'Errore sconosciuto')}`, "error");
        }
    };

    function renderStorages(storages) {
        if (!treeviewRoot) {
            console.error("TreeviewCtrl: Elemento treeview-root non trovato.");
            return;
        }
        treeviewRoot.innerHTML = ''; 
        if (!Array.isArray(storages)) {
            console.error("TreeviewCtrl: renderStorages si aspetta un array.", storages);
            return;
        }
        storages.forEach(storageCfg => {
            const li = document.createElement('li');
            li.classList.add('directory'); 
            li.textContent = storageCfg.name;
            li.dataset.storageName = storageCfg.name;
            li.dataset.path = ''; 
            li.dataset.storageType = storageCfg.type;
            li.dataset.isDir = "true"; 

            li.addEventListener('click', handleTreeviewItemClick);

            const ul = document.createElement('ul'); 
            li.appendChild(ul);
            treeviewRoot.appendChild(li);
        });
    }

    async function handleTreeviewItemClick(event) {
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
        const isDir = clickedElement.dataset.isDir === "true";

        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
            console.debug(`TreeviewCtrl: Click su ${isDir ? 'directory' : 'file'}: ${storageName}:${itemPath || '/'}`);
        }

        // Notifica app_logic o un gestore globale del cambio di selezione
        if (window.app_logic && typeof window.app_logic.setCurrentPathGlobal === 'function') { 
            window.app_logic.setCurrentPathGlobal(storageName, itemPath, storageType);
        } else if (typeof window.handleTreeviewSelect === 'function') { // Fallback
             window.handleTreeviewSelect(storageName, itemPath, storageType);
        } else if (window.config && config.IsLogLevel(config.LogLevelWarning)){
            console.warn("TreeviewCtrl: Nessuna funzione globale (app_logic.setCurrentPathGlobal o handleTreeviewSelect) trovata per notificare il cambio di path.");
        }


        if (isDir) {
            toggleDirectory(clickedElement, storageName, itemPath, storageType);
        }
    }

    async function toggleDirectory(directoryElement, storageName, dirPath, storageType) {
        const isOpen = directoryElement.classList.toggle('open');
        const ul = directoryElement.querySelector('ul');

        if (!ul) {
            console.error("TreeviewCtrl: Elemento <ul> mancante per la directory:", directoryElement);
            return;
        }

        if (isOpen && ul.children.length === 0) { 
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                console.info(`TreeviewCtrl: Espansione directory ${storageName}:${dirPath || '/'}. Caricamento contenuto...`);
            }
            notifyAppLogic(`Caricamento contenuto di ${dirPath || storageName}...`);
            
            if (!isDataServiceReady || !window.dataService || typeof window.dataService.fetchPage !== 'function') {
                console.error("TreeviewCtrl - ERRORE CRITICO in toggleDirectory: window.dataService.fetchPage non disponibile.");
                notifyAppLogic("Errore: Servizio dati non pronto per caricare contenuto directory.", "error");
                directoryElement.classList.remove('open'); 
                return;
            }

            try {
                const itemsPerPageForTree = 1000; 
                const options = { onlyDirectories: true }; 

                const payload = await window.dataService.fetchPage(storageName, dirPath, 1, itemsPerPageForTree, options);
                
                if (payload && payload.items) {
                    renderDirectoryContents(ul, payload.items, storageName, storageType);
                    if (payload.items.length === 0 && window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                        console.info(`TreeviewCtrl: La directory ${storageName}:${dirPath || '/'} è vuota.`);
                    }
                } else {
                     throw new Error("Payload non valido o items mancanti dalla risposta del dataService.");
                }
            } catch (error) {
                console.error(`TreeviewCtrl: Errore caricamento contenuto directory ${storageName}:${dirPath || '/'}:`, error);
                let errorMessage = "Errore sconosciuto";
                if (error && error.message) errorMessage = error.message;
                else if (error && error.error) errorMessage = error.error; // Se l'errore è un oggetto con una proprietà 'error'
                notifyAppLogic(`Errore caricamento ${dirPath || storageName}: ${errorMessage}`, 'error');
                directoryElement.classList.remove('open'); 
                ul.innerHTML = `<li>Errore caricamento: ${errorMessage}</li>`;
            }
        } else if (!isOpen) {
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                console.debug(`TreeviewCtrl: Compressione directory ${storageName}:${dirPath || '/'}.`);
            }
        }
    }

    function renderDirectoryContents(parentElementUL, items, currentStorageName, currentStorageType) {
        parentElementUL.innerHTML = ''; 
        if (!Array.isArray(items)) {
            console.error("TreeviewCtrl: renderDirectoryContents si aspetta un array di items.", items);
            return;
        }

        items.sort((a, b) => { 
            if (a.is_dir && !b.is_dir) return -1;
            if (!a.is_dir && b.is_dir) return 1;
            return a.name.localeCompare(b.name);
        });

        items.forEach(item => {
            const li = document.createElement('li');
            li.textContent = item.name;
            li.dataset.storageName = currentStorageName;
            li.dataset.path = item.path; 
            li.dataset.storageType = currentStorageType;
            li.dataset.isDir = item.is_dir.toString();

            if (item.is_dir) {
                li.classList.add('directory');
                const ul = document.createElement('ul');
                li.appendChild(ul);
            }
            li.addEventListener('click', handleTreeviewItemClick);
            parentElementUL.appendChild(li);
        });
    }

    window.expandAllTreeviewNodes = () => {
        if (!treeviewRoot) return;
        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
            console.info("TreeviewCtrl: Espansione di tutti i nodi (solo quelli già caricati nel DOM).");
        }
        treeviewRoot.querySelectorAll('li.directory:not(.open)').forEach(dirEl => {
            if (!dirEl.classList.contains('open')) { 
                 dirEl.click(); 
            }
        });
    };

    window.collapseAllTreeviewNodes = () => {
        if (!treeviewRoot) return;
        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
            console.info("TreeviewCtrl: Compressione di tutti i nodi.");
        }
        treeviewRoot.querySelectorAll('li.directory.open').forEach(dirEl => {
            dirEl.classList.remove('open');
        });
    };

    if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
        console.info("treeview_controller.js caricato.");
    }
})();
