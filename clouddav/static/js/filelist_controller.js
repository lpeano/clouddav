// Si assume che window.config, window.dataService, e window.app_logic siano/saranno definiti globalmente.
// Questo script attenderà che dataService e appLogic siano pronti.

// --------------- filelist_controller.js ---------------
(() => {
    if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
        console.info("--- EXECUTING filelist_controller.js ---");
    }

    const filelistTableBody = document.querySelector("#filelist-table tbody");
    const currentPathArea = document.getElementById("current-path-area");
    const currentPageSpan = document.getElementById("current-page");
    const totalPagesSpan = document.getElementById("total-pages");
    const prevPageBtn = document.getElementById("prev-page-btn");
    const nextPageBtn = document.getElementById("next-page-btn");
    const createFolderBtn = document.getElementById("create-folder-btn");
    const uploadTriggerBtn = document.getElementById("trigger-upload-btn");
    const uploadFileInput = document.getElementById("upload-file-input");
    
    const enableNameFilterCheckbox = document.getElementById('enable-name-filter');
    const nameFilterInput = document.getElementById('name-filter');
    const enableTimestampFilterCheckbox = document.getElementById('enable-timestamp-filter');
    const timestampFilterInput = document.getElementById('timestamp-filter');
    const applyFiltersBtn = document.getElementById('apply-filters-btn');
    const clearFiltersBtn = document.getElementById('clear-filters-btn');

    let currentStorage = null;
    let currentPath = null;
    let currentStorageType = null;
    let currentPage = 1;
    let totalItems = 0;
    let itemsPerPage = 50; 

    let isDataServiceReadyForFilelist = (window.data_service_ready_flag && typeof window.dataService === 'object' && window.dataService !== null) || false;
    let isAppLogicReadyForFilelist = (window.app_logic_ready_flag && typeof window.app_logic === 'object' && window.app_logic !== null) || false;

    function notify(message, type = 'info') {
        const localAppLogicReady = (window.app_logic_ready_flag && typeof window.app_logic === 'object' && window.app_logic !== null);
        if (localAppLogicReady && window.app_logic && typeof window.app_logic.addMessageToHistory === 'function') {
            window.app_logic.addMessageToHistory(`Filelist: ${message}`, type);
        }
        if (window.showToast && typeof window.showToast === 'function') {
            window.showToast(`Filelist: ${message}`, type);
        } else if (window.config && config.IsLogLevel(config.LogLevelWarning)){
            console.warn("FilelistCtrl: window.showToast non definito, impossibile mostrare notifica UI.");
        }
    }

    if (!isDataServiceReadyForFilelist) {
        window.addEventListener('dataServiceReady', () => {
            if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info("FilelistCtrl: Evento 'dataServiceReady' ricevuto.");
            if (typeof window.dataService === 'object' && window.dataService !== null) {
                isDataServiceReadyForFilelist = true;
            } else {
                console.error("FilelistCtrl: Evento 'dataServiceReady' ricevuto, MA window.dataService non è un oggetto valido!");
            }
        }, { once: true });
    }
    if (!isAppLogicReadyForFilelist) {
        window.addEventListener('appLogicReady', () => {
            if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info("FilelistCtrl: Evento 'appLogicReady' ricevuto.");
             if (typeof window.app_logic === 'object' && window.app_logic !== null) {
                isAppLogicReadyForFilelist = true;
            } else {
                console.error("FilelistCtrl: Evento 'appLogicReady' ricevuto, MA window.app_logic non è un oggetto valido!");
            }
        }, { once: true });
    }

    function formatSize(bytes) {
        if (bytes === 0) return '0 B';
        if (!bytes || isNaN(bytes)) return '-';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    }

    function formatDate(dateString) {
        if (!dateString || dateString === '0001-01-01T00:00:00Z') return '-';
        try {
            const date = new Date(dateString);
            return date.toLocaleString([], { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
        } catch (e) { return dateString; }
    }

    function renderFilelist(items) {
        if (!filelistTableBody) {
            console.error("FilelistCtrl: Elemento filelistTableBody non trovato.");
            return;
        }
        filelistTableBody.innerHTML = ''; 
        if (!items || items.length === 0) {
            const tr = filelistTableBody.insertRow();
            const td = tr.insertCell();
            td.colSpan = 5; 
            td.textContent = 'Nessun file o cartella in questa directory.';
            td.style.textAlign = 'center';
            return;
        }

        items.forEach(item => {
            const row = filelistTableBody.insertRow();
            row.dataset.name = item.name; row.dataset.path = item.path; row.dataset.isDir = item.is_dir.toString();
            const cellName = row.insertCell();
            cellName.textContent = item.name; cellName.dataset.isDir = item.is_dir.toString();
            if (item.is_dir) {
                cellName.style.cursor = 'pointer'; cellName.style.color = '#007bff';
                cellName.addEventListener('click', () => {
                    const localAppLogicReady = (window.app_logic_ready_flag && typeof window.app_logic === 'object' && window.app_logic !== null);
                    if (localAppLogicReady && window.app_logic.setCurrentPathGlobal) {
                        window.app_logic.setCurrentPathGlobal(currentStorage, item.path, currentStorageType);
                    } else {
                        console.error("FilelistCtrl: app_logic.setCurrentPathGlobal non disponibile al click sulla directory.");
                        notify("Errore: Impossibile navigare nella cartella.", "error");
                    }
                });
            }
            const cellType = row.insertCell(); cellType.textContent = item.is_dir ? 'Cartella' : 'File';
            const cellSize = row.insertCell(); cellSize.textContent = item.is_dir ? '-' : formatSize(item.size);
            const cellModified = row.insertCell(); cellModified.textContent = formatDate(item.mod_time);
            const cellActions = row.insertCell(); cellActions.classList.add('file-actions');
            if (!item.is_dir) {
                const downloadBtn = document.createElement('button'); downloadBtn.textContent = 'Download';
                downloadBtn.onclick = () => {
                    if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info(`FilelistCtrl: Download richiesto per ${currentStorage}:${item.path}`);
                    const downloadUrl = `/download?storage=${encodeURIComponent(currentStorage)}&path=${encodeURIComponent(item.path)}`;
                    window.open(downloadUrl, '_blank');
                };
                cellActions.appendChild(downloadBtn);
            }
            const deleteBtn = document.createElement('button'); deleteBtn.textContent = 'Elimina'; deleteBtn.style.marginLeft = '5px';
            deleteBtn.onclick = () => {
                if (window.app_logic && typeof window.app_logic.confirmDeleteItem === 'function') {
                     window.app_logic.confirmDeleteItem(currentStorage, item.path, item.name, item.is_dir);
                } else if (typeof window.confirmDeleteItem === 'function') { 
                    window.confirmDeleteItem(currentStorage, item.path, item.name, item.is_dir);
                } else {
                    console.warn("FilelistCtrl: window.app_logic.confirmDeleteItem o window.confirmDeleteItem non definito.");
                    notify("Funzione di eliminazione non disponibile.", "warning");
                }
            };
            cellActions.appendChild(deleteBtn);
        });
    }

    function updatePaginationControls(currentPageNum, totalItemsNum, itemsPerPageNum) {
        currentPage = parseInt(currentPageNum, 10) || 1;
        totalItems = parseInt(totalItemsNum, 10) || 0;
        itemsPerPage = parseInt(itemsPerPageNum, 10) || 50;
        if (!currentPageSpan || !totalPagesSpan || !prevPageBtn || !nextPageBtn) return;
        currentPageSpan.textContent = currentPage;
        const totalPgs = Math.ceil(totalItems / itemsPerPage) || 1;
        totalPagesSpan.textContent = totalPgs;
        prevPageBtn.disabled = currentPage <= 1;
        nextPageBtn.disabled = currentPage >= totalPgs;
    }

    async function loadDirectoryData(storage, path, page = 1, filterOptions = {}) {
        const localDataServiceReady = (window.data_service_ready_flag && typeof window.dataService === 'object' && window.dataService !== null);
        if (!localDataServiceReady || typeof window.dataService.fetchPage !== 'function') {
            console.error(`FilelistCtrl: dataService (pronto: ${localDataServiceReady}) o dataService.fetchPage non disponibile per loadDirectoryData.`);
            if(window.config && config.IsLogLevel(config.LogLevelDebug)){
                console.debug("FilelistCtrl: Stato di window.data_service_ready_flag:", window.data_service_ready_flag);
                console.debug("FilelistCtrl: Stato di window.dataService:", window.dataService);
                if(window.dataService) console.debug("FilelistCtrl: Tipo di window.dataService.fetchPage:", typeof window.dataService.fetchPage);
            }
            notify("Errore: Servizio dati non pronto.", "error");
            if (window.hideFilelistLoadingSpinner) window.hideFilelistLoadingSpinner();
            return;
        }
        if (window.showFilelistLoadingSpinner) window.showFilelistLoadingSpinner();

        if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
            console.info(`FilelistCtrl: Caricamento directory: ${storage}:${path || '/'}, Pagina: ${page}, Filtri:`, filterOptions);
        }
        
        const options = { 
            onlyDirectories: false, 
            nameFilter: filterOptions.nameFilter || "",
            timestampFilter: filterOptions.timestampFilter || ""
        };

        try {
            const payload = await window.dataService.fetchPage(storage, path, page, itemsPerPage, options);
            if (payload && payload.items) {
                if (window.config && config.IsLogLevel(config.LogLevelDebug)) console.debug("FilelistCtrl: Payload ricevuto da dataService.fetchPage:", payload);
                renderFilelist(payload.items);
                updatePaginationControls(payload.page, payload.totalItems, payload.itemsPerPage);
                itemsPerPage = payload.items_per_page || itemsPerPage; 
            } else {
                console.error("FilelistCtrl: Payload non valido o items mancanti da dataService.fetchPage", payload);
                notify("Errore nel formato dei dati ricevuti.", "error");
                renderFilelist([]); 
                updatePaginationControls(1, 0, itemsPerPage);
            }
        } catch (error) {
            console.error(`FilelistCtrl: Errore caricamento directory ${storage}:${path || '/'}:`, error);
            let errMsg = "Errore sconosciuto";
            if (error && error.message) errMsg = error.message;
            else if (error && error.error) errMsg = error.error; 
            notify(`Errore caricamento: ${errMsg}`, "error");
            renderFilelist([]); 
            updatePaginationControls(1, 0, itemsPerPage);
        } finally {
            if (window.hideFilelistLoadingSpinner) window.hideFilelistLoadingSpinner();
        }
    }

    window.filelist_controller = {
        handlePathChange: (newStorage, newPath, newStorageType) => {
            if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
                console.info(`FilelistCtrl: handlePathChange ricevuto: Storage='${newStorage}', Path='${newPath || '/'}', Type='${newStorageType}'`);
            }
            currentStorage = newStorage;
            currentPath = newPath;
            currentStorageType = newStorageType;
            currentPage = 1; 

            if (currentPathArea) {
                currentPathArea.textContent = `Percorso Corrente: ${currentStorage}${currentPath ? (':' + currentPath) : ''}`;
            }
            if (createFolderBtn) {
                createFolderBtn.style.display = currentStorage ? 'inline-block' : 'none';
            }
            
            const nameFilterVal = enableNameFilterCheckbox && enableNameFilterCheckbox.checked ? (nameFilterInput ? nameFilterInput.value : "") : "";
            const timestampFilterVal = enableTimestampFilterCheckbox && enableTimestampFilterCheckbox.checked ? (timestampFilterInput ? timestampFilterInput.value : "") : "";
            
            loadDirectoryData(currentStorage, currentPath, currentPage, {
                nameFilter: nameFilterVal,
                timestampFilter: timestampFilterVal ? new Date(timestampFilterVal).toISOString() : ""
            });
        },
        refreshCurrentView: () => {
            if (currentStorage && currentPath !== null) {
                if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
                    console.info(`FilelistCtrl: Refresh richiesto per ${currentStorage}:${currentPath || '/'}, Pagina: ${currentPage}`);
                }
                const nameFilterVal = enableNameFilterCheckbox && enableNameFilterCheckbox.checked ? (nameFilterInput ? nameFilterInput.value : "") : "";
                const timestampFilterVal = enableTimestampFilterCheckbox && enableTimestampFilterCheckbox.checked ? (timestampFilterInput ? timestampFilterInput.value : "") : "";
                loadDirectoryData(currentStorage, currentPath, currentPage, {
                    nameFilter: nameFilterVal,
                    timestampFilter: timestampFilterVal ? new Date(timestampFilterVal).toISOString() : ""
                });
            } else if (window.config && config.IsLogLevel(config.LogLevelWarning)) {
                console.warn("FilelistCtrl: Refresh richiesto ma currentStorage o currentPath non impostati.");
            }
        }
    };

    if (prevPageBtn) {
        prevPageBtn.addEventListener('click', () => {
            if (currentPage > 1) {
                currentPage--;
                window.filelist_controller.refreshCurrentView();
            }
        });
    }
    if (nextPageBtn) {
        nextPageBtn.addEventListener('click', () => {
            currentPage++;
            window.filelist_controller.refreshCurrentView();
        });
    }
    if (applyFiltersBtn) {
        applyFiltersBtn.addEventListener('click', () => {
            currentPage = 1; 
            window.filelist_controller.refreshCurrentView();
        });
    }
    if (clearFiltersBtn) {
        clearFiltersBtn.addEventListener('click', () => {
            if (enableNameFilterCheckbox) enableNameFilterCheckbox.checked = false;
            if (nameFilterInput) nameFilterInput.value = '';
            if (enableTimestampFilterCheckbox) enableTimestampFilterCheckbox.checked = false;
            if (timestampFilterInput) timestampFilterInput.value = '';
            currentPage = 1;
            window.filelist_controller.refreshCurrentView();
        });
    }

    if (createFolderBtn) {
        createFolderBtn.addEventListener('click', () => {
            if (window.app_logic && typeof window.app_logic.openCreateFolderModal === 'function') {
                window.app_logic.openCreateFolderModal(currentStorage, currentPath);
            } else {
                console.warn("FilelistCtrl: window.app_logic.openCreateFolderModal non definito.");
                notify("Funzionalità 'Nuova Cartella' non disponibile.", "warning");
            }
        });
    }
    if (uploadTriggerBtn && uploadFileInput) {
        uploadTriggerBtn.addEventListener('click', () => {
            if (!currentStorage) {
                notify("Seleziona prima uno storage e una directory.", "warning");
                return;
            }
            if (uploadFileInput.files.length === 0) {
                notify("Seleziona prima i file da caricare.", "info");
                uploadFileInput.click(); // Apri il selettore file se nessun file è selezionato
                return;
            }

            // Assicurati che app_logic sia pronto prima di chiamare le sue funzioni
            const localAppLogicReady = (window.app_logic_ready_flag && typeof window.app_logic === 'object' && window.app_logic !== null);
            if (!localAppLogicReady) {
                console.error("FilelistCtrl: app_logic non pronto per gestire l'upload.");
                notify("Servizio applicazione non ancora pronto per l'upload.", "error");
                return;
            }

            if (window.app_logic && typeof window.app_logic.openChunkSizeModalForUpload === 'function') {
                 window.app_logic.openChunkSizeModalForUpload(uploadFileInput.files, currentStorage, currentPath);
            } else {
                if (window.config && config.IsLogLevel(config.LogLevelWarning)) {
                    console.warn("FilelistCtrl: window.app_logic.openChunkSizeModalForUpload non definito. Tentativo di fallback a upload diretto.");
                }
                if (window.app_logic && typeof window.app_logic.handleFileUpload === 'function') {
                    notify("Avvio upload diretto (configurazione chunk di default)...", "info");
                    window.app_logic.handleFileUpload(uploadFileInput.files, currentStorage, currentPath, 4 * 1024 * 1024, 4); 
                } else {
                    console.error("FilelistCtrl: window.app_logic.handleFileUpload non definito. Funzionalità di upload non disponibile.");
                    notify("Funzionalità di upload non configurata. Contattare l'amministratore.", "error");
                }
            }
        });
    }

    if (window.config && config.IsLogLevel(config.LogLevelInfo)) {
        console.info("filelist_controller.js caricato e window.filelist_controller definito.");
    }
})();
