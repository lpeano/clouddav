// Si assume che window.config sia definito globalmente.
// Questo script attenderà che window.dataService e window.app_logic siano pronti.

// --------------- treeview_controller.js ---------------
(() => {
    if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
        console.info("--- PARSING treeview_controller.js ---");
    }

    const treeviewRoot = document.getElementById('treeview-root');
    let selectedTreeviewElement = null;
    
    let isDataServiceReadyForTreeview = (window.data_service_ready_flag && typeof window.dataService === 'object' && window.dataService !== null) || false;
    // Non fare affidamento solo sul flag per app_logic qui, lo controlleremo al momento dell'uso
    // let isAppLogicReadyForTreeview = window.app_logic_ready_flag || false; 

    function notify(message, type = 'info') {
        const localAppLogicReady = (window.app_logic_ready_flag && typeof window.app_logic === 'object' && window.app_logic !== null);
        if (localAppLogicReady && window.app_logic.addMessageToHistory) {
            window.app_logic.addMessageToHistory(`Treeview: ${message}`, type);
        }
        if (window.showToast && typeof window.showToast === 'function') {
            window.showToast(`Treeview: ${message}`, type);
        } else if (window.config && config.IsLogLevel(config.LogLevelDebug)){
             // console.debug("TreeviewCtrl: window.showToast non definito per notifica.");
        }
    }
    
    if (!isDataServiceReadyForTreeview) {
        window.addEventListener('dataServiceReady', () => {
            if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info("TreeviewCtrl: Evento 'dataServiceReady' ricevuto.");
            if (typeof window.dataService === 'object' && window.dataService !== null) {
                isDataServiceReadyForTreeview = true;
            } else {
                console.error("TreeviewCtrl: Evento 'dataServiceReady' ricevuto, MA window.dataService NON è un oggetto valido!");
            }
            attemptTreeviewInitialization(); // Ritenta l'init del treeview se necessario
        }, { once: true });
    }
    
    // Non è più necessario un listener separato per appLogicReady qui se controlliamo al momento dell'uso.
    // app_logic.js dovrebbe chiamare requestInitialTreeviewData quando TUTTO è pronto.

    function initializeTreeviewController() {
        if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
            console.info("TreeviewCtrl: Dipendenze pronte. TreeviewController inizializzato (o pronto per essere usato).");
        }
    }

    function attemptTreeviewInitialization() {
        const dataServiceActuallyReady = (window.data_service_ready_flag && typeof window.dataService === 'object' && window.dataService !== null);
        if (dataServiceActuallyReady) isDataServiceReadyForTreeview = true;

        // L'inizializzazione del treeview stesso (es. caricamento dati iniziali)
        // è ora guidata da app_logic.js che chiama window.requestInitialTreeviewData.
        // Questa funzione serve più a confermare che le dipendenze sono pronte per quando
        // le funzioni del treeview verranno chiamate.
        if (isDataServiceReadyForTreeview) {
            initializeTreeviewController();
        } else {
            if (window.config && config.IsLogLevel(config.LogLevelDebug)) {
                console.debug(`TreeviewCtrl: In attesa di dataService. Pronto: ${isDataServiceReadyForTreeview}`);
            }
        }
    }
    
    attemptTreeviewInitialization(); // Chiamata iniziale


    window.requestInitialTreeviewData = async function() { 
        if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info("TreeviewCtrl: Chiamata a requestInitialTreeviewData.");

        if (!isDataServiceReadyForTreeview || !window.dataService || typeof window.dataService.fetchFileSystems !== 'function') {
            const errorMsg = `TreeviewCtrl - ERRORE CRITICO: dataService (pronto: ${isDataServiceReadyForTreeview}, obj: ${!!window.dataService}) o dataService.fetchFileSystems non disponibile.`;
            console.error(errorMsg);
            notify("Errore: Servizio dati non pronto per caricare gli storage.", "error");
            return; 
        }

        try {
            if (window.config && config.IsLogLevel(config.LogLevelDebug)) console.debug("TreeviewCtrl: Chiamata a dataService.fetchFileSystems()");
            const storagesPayload = await window.dataService.fetchFileSystems(); 
            if (storagesPayload && Array.isArray(storagesPayload)) { 
                renderStorages(storagesPayload);
                notify("Elenco storage caricato.");
            } else {
                console.error("TreeviewCtrl: Payload da dataService.fetchFileSystems non è un array:", storagesPayload);
                notify("Errore formato dati storages da dataService.", "error");
            }
        } catch (errorPayload) {
            console.error("TreeviewCtrl: Errore da dataService.fetchFileSystems:", errorPayload);
            let errMsg = "Errore sconosciuto";
            if (errorPayload && errorPayload.message) errMsg = errorPayload.message;
            else if (errorPayload && errorPayload.error) errMsg = errorPayload.error;
            notify(`Errore caricamento storages: ${errMsg}`, "error");
        }
    };

    function renderStorages(storages) {
        if (!treeviewRoot) { console.error("TreeviewCtrl: Elemento treeview-root non trovato."); return; }
        treeviewRoot.innerHTML = ''; 
        if (!Array.isArray(storages)) { console.error("TreeviewCtrl: renderStorages si aspetta un array.", storages); return; }
        storages.forEach(storageCfg => {
            const li = document.createElement('li');
            li.classList.add('directory'); 
            li.textContent = storageCfg.name;
            li.dataset.storageName = storageCfg.name; li.dataset.path = ''; 
            li.dataset.storageType = storageCfg.type; li.dataset.isDir = "true"; 
            li.addEventListener('click', handleTreeviewItemClick);
            const ul = document.createElement('ul'); li.appendChild(ul);
            treeviewRoot.appendChild(li);
        });
    }

    async function handleTreeviewItemClick(event) {
        event.stopPropagation();
        const clickedElement = event.currentTarget; 

        if (selectedTreeviewElement) selectedTreeviewElement.classList.remove('selected');
        clickedElement.classList.add('selected');
        selectedTreeviewElement = clickedElement;

        const storageName = clickedElement.dataset.storageName;
        const itemPath = clickedElement.dataset.path;
        const storageType = clickedElement.dataset.storageType; 
        const isDir = clickedElement.dataset.isDir === "true";

        if (window.config && config.IsLogLevel(config.LogLevelDebug)) {
            console.debug(`TreeviewCtrl: Click su ${isDir ? 'directory' : 'file'}: ${storageName}:${itemPath || '/'}`);
            console.debug("TreeviewCtrl (handleTreeviewItemClick): Verifico window.app_logic:", window.app_logic);
            if(window.app_logic) {
                console.debug("TreeviewCtrl (handleTreeviewItemClick): Verifico typeof window.app_logic.setCurrentPathGlobal:", typeof window.app_logic.setCurrentPathGlobal);
            }
        }

        // CONTROLLO ROBUSTO AL MOMENTO DELL'USO
        if (window.app_logic && typeof window.app_logic.setCurrentPathGlobal === 'function') { 
            window.app_logic.setCurrentPathGlobal(storageName, itemPath, storageType);
        } else {
            console.warn(`TreeviewCtrl: window.app_logic.setCurrentPathGlobal non disponibile al momento del click. app_logic:`, window.app_logic);
            notify("Errore: Impossibile notificare cambio percorso all'applicazione principale.", "warning");
        }

        if (isDir) {
            toggleDirectory(clickedElement, storageName, itemPath, storageType);
        }
    }

    async function toggleDirectory(directoryElement, storageName, dirPath, storageType) {
        const isOpen = directoryElement.classList.toggle('open');
        const ul = directoryElement.querySelector('ul');
        if (!ul) { console.error("TreeviewCtrl: Elemento <ul> mancante per la directory:", directoryElement); return; }

        if (isOpen && ul.children.length === 0) { 
            if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info(`TreeviewCtrl: Espansione directory ${storageName}:${dirPath || '/'}. Caricamento contenuto...`);
            notify(`Caricamento contenuto di ${dirPath || storageName}...`);
            
            // CONTROLLO ROBUSTO AL MOMENTO DELL'USO
            if (!isDataServiceReadyForTreeview || !window.dataService || typeof window.dataService.fetchPage !== 'function') {
                console.error(`TreeviewCtrl - ERRORE CRITICO in toggleDirectory: dataService (pronto flag: ${isDataServiceReadyForTreeview}, obj: ${!!window.dataService}) o dataService.fetchPage non disponibile.`);
                if(window.config && config.IsLogLevel(config.LogLevelDebug)){
                    console.debug("TreeviewCtrl (toggleDirectory): Stato di window.data_service_ready_flag:", window.data_service_ready_flag);
                    console.debug("TreeviewCtrl (toggleDirectory): Stato di window.dataService:", window.dataService);
                    if(window.dataService) console.debug("TreeviewCtrl (toggleDirectory): Tipo di window.dataService.fetchPage:", typeof window.dataService.fetchPage);
                }
                notify("Errore: Servizio dati non pronto per caricare contenuto directory.", "error");
                directoryElement.classList.remove('open'); 
                return;
            }

            try {
                const itemsPerPageForTree = 1000; 
                const options = { onlyDirectories: true }; 
                const payload = await window.dataService.fetchPage(storageName, dirPath, 1, itemsPerPageForTree, options);
                if (payload && payload.items) {
                    renderDirectoryContents(ul, payload.items, storageName, storageType);
                    if (payload.items.length === 0 && window.config && config.IsLogLevel(config.LogLevelInfo)) console.info(`TreeviewCtrl: La directory ${storageName}:${dirPath || '/'} è vuota.`);
                } else { throw new Error("Payload non valido o items mancanti dalla risposta del dataService."); }
            } catch (error) {
                console.error(`TreeviewCtrl: Errore caricamento contenuto directory ${storageName}:${dirPath || '/'}:`, error);
                let errorMessage = "Errore sconosciuto";
                if (error && error.message) errorMessage = error.message;
                else if (error && error.error) errorMessage = error.error; 
                notify(`Errore caricamento ${dirPath || storageName}: ${errorMessage}`, 'error');
                directoryElement.classList.remove('open'); 
                ul.innerHTML = `<li>Errore caricamento: ${errorMessage}</li>`;
            }
        } else if (!isOpen) {
            if (window.config && config.IsLogLevel(config.LogLevelDebug)) console.debug(`TreeviewCtrl: Compressione directory ${storageName}:${dirPath || '/'}.`);
        }
    }

    function renderDirectoryContents(parentElementUL, items, currentStorageName, currentStorageType) {
        parentElementUL.innerHTML = ''; 
        if (!Array.isArray(items)) { console.error("TreeviewCtrl: renderDirectoryContents si aspetta un array.", items); return; }
        items.sort((a, b) => { 
            if (a.is_dir && !b.is_dir) return -1; if (!a.is_dir && b.is_dir) return 1;
            return a.name.localeCompare(b.name);
        });
        items.forEach(item => {
            const li = document.createElement('li');
            li.textContent = item.name;
            li.dataset.storageName = currentStorageName; li.dataset.path = item.path; 
            li.dataset.storageType = currentStorageType; li.dataset.isDir = item.is_dir.toString();
            if (item.is_dir) {
                li.classList.add('directory'); const ul = document.createElement('ul'); li.appendChild(ul);
            }
            li.addEventListener('click', handleTreeviewItemClick);
            parentElementUL.appendChild(li);
        });
    }

    window.expandAllTreeviewNodes = () => { 
        if (!treeviewRoot) return;
        if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info("TreeviewCtrl: Espansione nodi.");
        treeviewRoot.querySelectorAll('li.directory:not(.open)').forEach(dirEl => { if (!dirEl.classList.contains('open')) dirEl.click(); });
    };
    window.collapseAllTreeviewNodes = () => { 
        if (!treeviewRoot) return;
        if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info("TreeviewCtrl: Compressione nodi.");
        treeviewRoot.querySelectorAll('li.directory.open').forEach(dirEl => dirEl.classList.remove('open'));
    };

    if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
        console.info("treeview_controller.js caricato. In attesa di dataServiceReady e appLogicReady.");
    }
    attemptTreeviewInitialization(); 
})();
