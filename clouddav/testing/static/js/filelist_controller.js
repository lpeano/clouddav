// static/js/filelist_controller.js
// Manages the filelist logic.

(function() {
    const filelistTableBody = document.getElementById('filelist-table')?.querySelector('tbody');
    const uploadFileInput = document.getElementById('upload-file-input');
    const triggerUploadBtn = document.getElementById('trigger-upload-btn'); // New ID
    
    const nameFilterInput = document.getElementById('name-filter');
    const timestampFilterInput = document.getElementById('timestamp-filter');
    const enableNameFilterCheckbox = document.getElementById('enable-name-filter');
    const enableTimestampFilterCheckbox = document.getElementById('enable-timestamp-filter');
    const applyFiltersBtn = document.getElementById('apply-filters-btn');
    const clearFiltersBtn = document.getElementById('clear-filters-btn');

    const prevPageBtn = document.getElementById('prev-page-btn');
    const nextPageBtn = document.getElementById('next-page-btn');
    const currentPageSpan = document.getElementById('current-page');
    const totalPagesSpan = document.getElementById('total-pages');
    const currentPathArea = document.getElementById('current-path-area');

    let currentFilelistStorageName = '';
    let currentFilelistDirPath = '';
    let currentFilelistPage = 1;
    let itemsPerPageFilelist = 50; 
    let totalItemsFilelist = 0;
    let currentNameFilter = '';
    let currentTimestampFilter = '';
    window.lastFilelistRequestId = null; // Made global for app_logic.js to check

    const ongoingUploadsMap = new Map();

    function notifyAppLogic(message, type = 'info', details = {}) {
        if (window.addMessageToHistory) {
            window.addMessageToHistory(`Filelist: ${message}`, type);
        }
        // Use toast for user-facing notifications
        if (window.showToast && (type === 'success' || type === 'error' || type === 'warning')) {
            let toastMsg = message;
            if (details.filename) toastMsg = `${details.filename}: ${message}`;
            if (details.error) toastMsg += ` Dettagli: ${details.error}`;
            window.showToast(toastMsg, type);
        }
    }
    
    // Exposed to app_logic.js
    window.handleFilelistBackendResponse = (message) => {
        console.log('FilelistCtrl - Backend message received:', message);

        if (message.request_id && message.request_id !== window.lastFilelistRequestId && message.type === 'list_directory_response') {
            console.warn(`FilelistCtrl - Ignored obsolete list_directory_response (ID: ${message.request_id}). Expected: ${window.lastFilelistRequestId}`);
            if(window.hideFilelistLoadingSpinner) window.hideFilelistLoadingSpinner();
            return;
        }

        switch (message.type) {
            case 'list_directory_response':
                if (message.payload && Array.isArray(message.payload.items)) {
                    renderFileList(message.payload.items);
                    totalItemsFilelist = message.payload.total_items;
                    currentFilelistPage = message.payload.page;
                    itemsPerPageFilelist = message.payload.items_per_page;
                    updatePaginationControls();
                } else {
                    console.error('FilelistCtrl - Invalid list_directory_response payload:', message.payload);
                    notifyAppLogic('Errore nel caricare la lista file: dati non validi.', 'error');
                    renderFileList([]); 
                    totalItemsFilelist = 0;
                    currentFilelistPage = 1;
                    updatePaginationControls();
                }
                if(window.hideFilelistLoadingSpinner) window.hideFilelistLoadingSpinner();
                break;
            case 'create_directory_response':
                notifyAppLogic(`Cartella "${message.payload.name || message.payload.dir_path}" creata.`, 'success', {filename: message.payload.name});
                resetPaginationAndLoadFiles();
                break;
            case 'delete_item_response':
                notifyAppLogic(`Elemento "${message.payload.name || message.payload.item_path}" eliminato.`, 'success', {filename: message.payload.name});
                resetPaginationAndLoadFiles();
                break;
            case 'check_directory_contents_response':
                const { has_contents } = message.payload;
                const checkDetails = globalOngoingDeleteCheck; // Use global var from app_logic
                if (checkDetails) {
                    if (window.showGlobalDeleteConfirmModal) {
                        window.showGlobalDeleteConfirmModal({
                            ...checkDetails,
                            warningMessage: has_contents ?
                                `Questa cartella contiene elementi. Sei sicuro di voler eliminare "${checkDetails.itemName}" e tutto il suo contenuto? Questa azione non può essere annullata.` :
                                `Sei sicuro di voler eliminare "${checkDetails.itemName}"? Questa azione non può essere annullata.`
                        });
                    }
                }
                globalOngoingDeleteCheck = null; // Clear after use
                break;
            case 'error': // Errors specific to filelist operations might be handled here too
                console.error('FilelistCtrl - Error from backend:', message.payload.error);
                notifyAppLogic(`Operazione fallita: ${message.payload.error}`, 'error', {error: message.payload.error});
                if(window.hideFilelistLoadingSpinner) window.hideFilelistLoadingSpinner();
                break;
        }
    };
    
    // Exposed to app_logic.js
    window.loadFilelistForPath = (storageName, dirPath) => {
        currentFilelistStorageName = storageName;
        currentFilelistDirPath = dirPath;
        currentFilelistPage = 1; // Reset page on new path
        updateCurrentPathDisplay();
        if(window.showFilelistLoadingSpinner) window.showFilelistLoadingSpinner();

        if (window.sendMessage) {
            window.lastFilelistRequestId = window.sendMessage({
                type: 'list_directory',
                payload: {
                    storage_name: currentFilelistStorageName,
                    dir_path: currentFilelistDirPath,
                    page: currentFilelistPage,
                    items_per_page: itemsPerPageFilelist,
                    name_filter: currentNameFilter,
                    timestamp_filter: currentTimestampFilter
                }
            });
        } else {
            console.error('FilelistCtrl - sendMessage function is not available.');
            if(window.hideFilelistLoadingSpinner) window.hideFilelistLoadingSpinner();
        }
    };

    function updateCurrentPathDisplay() {
        const displayPath = currentFilelistDirPath === '' ? '/' : `/${currentFilelistDirPath}`;
        if(currentPathArea) currentPathArea.textContent = `Percorso Corrente: ${currentFilelistStorageName}${displayPath}`;
    }

    function resetPaginationAndLoadFiles() {
        currentFilelistPage = 1;
        window.loadFilelistForPath(currentFilelistStorageName, currentFilelistDirPath);
    }

    function renderFileList(items) {
        if (!filelistTableBody) return;
        filelistTableBody.innerHTML = '';

        if (currentFilelistDirPath !== '') {
            const tr = document.createElement('tr');
            const nameTd = document.createElement('td');
            nameTd.textContent = '..';
            nameTd.style.fontWeight = 'bold';
            nameTd.style.cursor = 'pointer';
            nameTd.colSpan = 5;
            nameTd.dataset.isdir = "true"; // Mark as directory for styling/logic
            nameTd.addEventListener('click', () => {
                const parentPath = currentFilelistDirPath.substring(0, currentFilelistDirPath.lastIndexOf('/'));
                window.loadFilelistForPath(currentFilelistStorageName, parentPath);
            });
            tr.appendChild(nameTd);
            filelistTableBody.appendChild(tr);
        }

        items.forEach(item => {
            const tr = document.createElement('tr');
            const nameTd = document.createElement('td');
            nameTd.textContent = item.name;
            if (item.is_dir) {
                nameTd.dataset.isdir = "true";
                nameTd.style.fontWeight = 'bold'; // Already handled by CSS, but kept for clarity
                nameTd.style.cursor = 'pointer'; // Already handled by CSS
                nameTd.addEventListener('click', () => {
                    window.loadFilelistForPath(currentFilelistStorageName, item.path);
                });
            }
            tr.appendChild(nameTd);

            const typeTd = document.createElement('td');
            typeTd.textContent = item.is_dir ? 'Directory' : 'File';
            tr.appendChild(typeTd);

            const sizeTd = document.createElement('td');
            sizeTd.textContent = item.is_dir ? '' : formatBytesForDisplay(item.size);
            tr.appendChild(sizeTd);

            const modTimeTd = document.createElement('td');
            const modDate = new Date(item.mod_time);
            modTimeTd.textContent = isNaN(modDate.getTime()) ? '' : modDate.toLocaleString();
            tr.appendChild(modTimeTd);

            const actionsTd = document.createElement('td');
            actionsTd.className = 'file-actions';
            if (!item.is_dir) {
                const downloadBtn = document.createElement('button');
                downloadBtn.textContent = 'Download';
                downloadBtn.addEventListener('click', () => downloadFile(currentFilelistStorageName, item.path));
                actionsTd.appendChild(downloadBtn);
            }
            const deleteBtn = document.createElement('button');
            deleteBtn.textContent = 'Elimina';
            deleteBtn.classList.add('delete-btn'); // Use class for styling
            deleteBtn.addEventListener('click', () => {
                const deleteDetails = {
                    storageName: currentFilelistStorageName,
                    itemPath: item.path,
                    itemName: item.name
                };
                if (item.is_dir) {
                    globalOngoingDeleteCheck = deleteDetails; // Store details for when response comes
                     if (window.sendMessage) {
                        window.sendMessage({
                            type: 'check_directory_contents_request',
                            payload: { storage_name: currentFilelistStorageName, dir_path: item.path }
                        });
                    }
                } else {
                    if(window.showGlobalDeleteConfirmModal) window.showGlobalDeleteConfirmModal(deleteDetails);
                }
            });
            actionsTd.appendChild(deleteBtn);
            tr.appendChild(actionsTd);
            filelistTableBody.appendChild(tr);
        });
    }

    function updatePaginationControls() {
        if (!currentPageSpan || !totalPagesSpan || !prevPageBtn || !nextPageBtn) return;
        const totalPages = Math.ceil(totalItemsFilelist / itemsPerPageFilelist) || 1;
        currentPageSpan.textContent = currentFilelistPage;
        totalPagesSpan.textContent = totalPages;
        prevPageBtn.disabled = currentFilelistPage <= 1;
        nextPageBtn.disabled = currentFilelistPage >= totalPages;
    }

    if(prevPageBtn) prevPageBtn.addEventListener('click', () => {
        if (currentFilelistPage > 1) {
            currentFilelistPage--;
            window.loadFilelistForPath(currentFilelistStorageName, currentFilelistDirPath);
        }
    });
    if(nextPageBtn) nextPageBtn.addEventListener('click', () => {
        const totalPages = Math.ceil(totalItemsFilelist / itemsPerPageFilelist);
        if (currentFilelistPage < totalPages) {
            currentFilelistPage++;
            window.loadFilelistForPath(currentFilelistStorageName, currentFilelistDirPath);
        }
    });

    function applyFilters() {
        currentNameFilter = enableNameFilterCheckbox.checked ? nameFilterInput.value : '';
        if (enableTimestampFilterCheckbox.checked && timestampFilterInput.value) {
            try {
                const date = new Date(timestampFilterInput.value);
                currentTimestampFilter = isNaN(date.getTime()) ? '' : date.toISOString();
                if (currentTimestampFilter === '' && timestampFilterInput.value) {
                     notifyAppLogic('Formato data/ora per filtro non valido.', 'warning');
                }
            } catch (e) { currentTimestampFilter = ''; }
        } else {
            currentTimestampFilter = '';
        }
        resetPaginationAndLoadFiles();
    }
    if(applyFiltersBtn) applyFiltersBtn.addEventListener('click', applyFilters);

    function clearFilters() {
        if(nameFilterInput) nameFilterInput.value = '';
        if(timestampFilterInput) timestampFilterInput.value = '';
        if(enableNameFilterCheckbox) enableNameFilterCheckbox.checked = false;
        if(enableTimestampFilterCheckbox) enableTimestampFilterCheckbox.checked = false;
        currentNameFilter = '';
        currentTimestampFilter = '';
        resetPaginationAndLoadFiles();
    }
    if(clearFiltersBtn) clearFiltersBtn.addEventListener('click', clearFilters);

    function formatBytesForDisplay(bytes, decimals = 2) {
        if (bytes === 0) return '0 Bytes';
        const k = 1024;
        const dm = decimals < 0 ? 0 : decimals;
        const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
    }

    function downloadFile(storageName, filePath) {
        notifyAppLogic(`Download di ${filePath.split('/').pop()} avviato...`, 'info', {filename: filePath.split('/').pop()});
        const downloadUrl = `/download?storage=${encodeURIComponent(storageName)}&path=${encodeURIComponent(filePath)}`;
        window.open(downloadUrl, '_blank');
    }

    if(triggerUploadBtn) triggerUploadBtn.addEventListener('click', () => {
        const files = uploadFileInput.files;
        if (files.length === 0) {
            notifyAppLogic('Seleziona uno o più file da caricare.', 'warning');
            return;
        }
        if (currentFilelistStorageName === '' || currentFilelistDirPath === undefined) {
            notifyAppLogic('Seleziona una cartella di destinazione nel treeview.', 'warning');
            return;
        }
        if (window.showGlobalChunkSizeModal) {
            window.showGlobalChunkSizeModal(Array.from(files));
        }
    });
    
    async function calculateSHA256ForFile(file) {
        if (!file) return null;
        try {
            const buffer = await file.arrayBuffer();
            const hashBuffer = await crypto.subtle.digest('SHA-256', buffer);
            const hashArray = Array.from(new Uint8Array(hashBuffer));
            return hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
        } catch (error) {
            console.error(`FilelistCtrl - Errore nel calcolo SHA256 per ${file.name}:`, error);
            return null;
        }
    }

    // Exposed to app_logic.js
    window.initiateFileUploads = async (files, chunkSize, parallelChunks) => {
        for (let i = 0; i < files.length; i++) {
            const file = files[i];
            const uploadId = `${Date.now()}-${i}-${file.name.replace(/[^a-zA-Z0-9.-]/g, '')}`;
            const filePath = currentFilelistDirPath === '' ? file.name : `${currentFilelistDirPath}/${file.name}`;
            
            if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, file.name, 0, 'Inizio calcolo SHA256...', filePath);
                const clientSHA256 = await calculateSHA256ForFile(file);
            if (!clientSHA256) {
                if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, file.name, 0, 'Errore calcolo SHA256.', filePath, true, 'failed');
                continue;
            }
            if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, file.name, 0, 'SHA256 calcolato. Preparazione...', filePath);

            ongoingUploadsMap.set(uploadId, {
                file, chunkSize, parallelChunks, clientSHA256,
                storageName: currentFilelistStorageName, filePath,
                uploadedSize: 0, blockIDs: [], expectedFileSize: file.size,
                isUploading: true, activeChunkUploads: 0, chunkQueue: [],
                activeXHRs: new Set(),
                resolve: null, reject: null // Promises will be set up per upload
            });
            
            // Start the actual upload process for this file
            startSingleFileUpload(uploadId);
        }
    };

    async function startSingleFileUpload(uploadId) {
        const uploadState = ongoingUploadsMap.get(uploadId);
        if (!uploadState) return;

        new Promise(async (resolve, reject) => {
            uploadState.resolve = resolve;
            uploadState.reject = reject;

            try {
                const initiateResponse = await fetch('/upload', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: new URLSearchParams({
                        storage: uploadState.storageName,
                        path: uploadState.filePath,
                        action: 'initiate',
                        total_file_size: uploadState.expectedFileSize.toString(),
                        chunk_size: uploadState.chunkSize.toString()
                    })
                });

                if (!initiateResponse.ok) {
                    const errorText = await initiateResponse.text();
                    throw new Error(`Errore inizializzazione upload: ${initiateResponse.status} - ${errorText}`);
                }
                const data = await initiateResponse.json();
                uploadState.uploadedSize = data.uploaded_size || 0;
                
                const initialPercentage = (uploadState.uploadedSize / uploadState.expectedFileSize) * 100;
                if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, uploadState.file.name, initialPercentage, uploadState.uploadedSize > 0 ? 'Ripresa upload...' : 'Caricamento...', uploadState.filePath);

                let currentOffset = uploadState.uploadedSize;
                let currentChunkIndex = Math.floor(uploadState.uploadedSize / uploadState.chunkSize);
                while (currentOffset < uploadState.expectedFileSize) {
                    const chunk = uploadState.file.slice(currentOffset, currentOffset + uploadState.chunkSize);
                    const blockID = btoa(String(currentChunkIndex).padStart(20, '0')); // For Azure Blob
                    uploadState.chunkQueue.push({ chunk, blockID, index: currentChunkIndex });
                    currentOffset += uploadState.chunkSize;
                    currentChunkIndex++;
                }
                for (let i = 0; i < uploadState.parallelChunks; i++) {
                    processNextChunkInternal(uploadId);
                }
            } catch (error) {
                console.error(`FilelistCtrl - Errore critico durante l'upload ID ${uploadId}:`, error);
                if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, uploadState.file.name, uploadState.uploadedSize / uploadState.expectedFileSize * 100, `Errore: ${error.message}`, uploadState.filePath, true, 'failed');
                uploadState.isUploading = false;
                ongoingUploadsMap.delete(uploadId);
                reject(error); // Reject the promise for this upload
            }
        }).catch(err => {
            console.error(`FilelistCtrl - Upload fallito per ${uploadState.file.name}: ${err.message}`);
            // Ensure UI is updated if not already
            if(window.updateGlobalUploadProgress && uploadState.isUploading) { // Check isUploading to avoid double notification
                 window.updateGlobalUploadProgress(uploadId, uploadState.file.name, uploadState.uploadedSize / uploadState.expectedFileSize * 100, `Fallito: ${err.message}`, uploadState.filePath, true, 'failed');
            }
            uploadState.isUploading = false; // Ensure it's marked as not uploading
            ongoingUploadsMap.delete(uploadId); // Clean up
        });
    }

    async function processNextChunkInternal(uploadId) {
        const uploadState = ongoingUploadsMap.get(uploadId);
        if (!uploadState || !uploadState.isUploading) return;

        if (uploadState.chunkQueue.length === 0 && uploadState.activeChunkUploads === 0) {
            finalizeUploadInternal(uploadId);
            return;
        }
        if (uploadState.activeChunkUploads >= uploadState.parallelChunks || uploadState.chunkQueue.length === 0) {
            return;
        }

        const { chunk, blockID, index: chunkIndex } = uploadState.chunkQueue.shift();
        uploadState.activeChunkUploads++;

        const formData = new FormData();
        formData.append('storage', uploadState.storageName);
        formData.append('path', uploadState.filePath);
        formData.append('action', 'chunk');
        formData.append('block_id', blockID); // For Azure
        formData.append('chunk_index', chunkIndex.toString()); // For Local
        formData.append('chunk_size', uploadState.chunkSize.toString()); // For Local
        formData.append('chunk', chunk);

        const xhr = new XMLHttpRequest();
        uploadState.activeXHRs.add(xhr);
        
        xhr.open('POST', '/upload', true);
        xhr.timeout = 300000; // 5 minutes timeout for chunk

        if (xhr.upload) {
            xhr.upload.onprogress = (event) => {
                if (event.lengthComputable) {
                    const chunkProgress = event.loaded; // Progress within the current chunk
                    const overallUploaded = uploadState.uploadedSize + chunkProgress;
                    const percentage = (overallUploaded / uploadState.expectedFileSize) * 100;
                    if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, uploadState.file.name, percentage, `Caricamento chunk ${chunkIndex + 1}...`, uploadState.filePath);
                }
            };
        }

        xhr.onload = () => {
            uploadState.activeXHRs.delete(xhr);
            uploadState.activeChunkUploads--;
            if (xhr.status >= 200 && xhr.status < 300) {
                if (uploadState.isUploading) {
                    uploadState.uploadedSize += chunk.size; // Crucial: update uploadedSize only after successful chunk upload
                    uploadState.blockIDs.push(blockID); // For Azure
                    const percentage = (uploadState.uploadedSize / uploadState.expectedFileSize) * 100;
                    if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, uploadState.file.name, percentage, `Chunk ${chunkIndex + 1} caricato.`, uploadState.filePath);
                }
            } else {
                const errorText = xhr.responseText || `Errore HTTP: ${xhr.status}`;
                console.error(`FilelistCtrl - Errore caricamento chunk ${chunkIndex} per ${uploadState.file.name}: ${errorText}`);
                uploadState.isUploading = false; // Stop further processing for this file
                uploadState.reject(new Error(errorText)); // Reject the promise for this upload
                return; // Stop processing more chunks for this failed upload
            }
            processNextChunkInternal(uploadId); // Process next chunk
        };
        xhr.onerror = () => {
            uploadState.activeXHRs.delete(xhr);
            uploadState.activeChunkUploads--;
            const errorMsg = 'Errore di rete durante caricamento chunk.';
            console.error(`FilelistCtrl - ${errorMsg} (Chunk ${chunkIndex}, File: ${uploadState.file.name})`);
            uploadState.isUploading = false;
            uploadState.reject(new Error(errorMsg));
        };
        xhr.onabort = () => { // Handle user cancellation
            uploadState.activeXHRs.delete(xhr);
            uploadState.activeChunkUploads--;
            console.log(`FilelistCtrl - Upload chunk ${chunkIndex} annullato per ${uploadState.file.name}.`);
            // No reject here, as cancellation is handled by cancelUploadFile
            processNextChunkInternal(uploadId); // Allow other parallel uploads to continue if any
        };
        xhr.ontimeout = () => {
            uploadState.activeXHRs.delete(xhr);
            uploadState.activeChunkUploads--;
            const errorMsg = 'Timeout durante caricamento chunk.';
            console.error(`FilelistCtrl - ${errorMsg} (Chunk ${chunkIndex}, File: ${uploadState.file.name})`);
            uploadState.isUploading = false;
            uploadState.reject(new Error(errorMsg));
        };
        xhr.send(formData);
    }

    async function finalizeUploadInternal(uploadId) {
        const uploadState = ongoingUploadsMap.get(uploadId);
        if (!uploadState || !uploadState.isUploading) return;

        if (uploadState.uploadedSize < uploadState.expectedFileSize) {
             const errorMsg = "Finalizzazione fallita: non tutti i chunk sono stati caricati.";
             console.warn(`FilelistCtrl - ${errorMsg} (File: ${uploadState.file.name})`);
             uploadState.reject(new Error(errorMsg));
             return;
        }

        if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, uploadState.file.name, 100, 'Finalizzazione e verifica...', uploadState.filePath);
        try {
            const finalizeResponse = await fetch('/upload', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: new URLSearchParams({
                    storage: uploadState.storageName,
                    path: uploadState.filePath,
                    action: 'finalize',
                    block_ids: JSON.stringify(uploadState.blockIDs), // For Azure
                    client_sha256: uploadState.clientSHA256,
                    total_file_size: uploadState.expectedFileSize.toString()
                })
            });
            if (!finalizeResponse.ok) {
                const errorText = await finalizeResponse.text();
                throw new Error(`Errore finalizzazione: ${finalizeResponse.status} - ${errorText}`);
            }
            if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, uploadState.file.name, 100, 'Completato!', uploadState.filePath, true, 'complete');
            notifyAppLogic(`Upload di "${uploadState.file.name}" completato.`, 'success', {filename: uploadState.file.name});
            uploadState.resolve(); // Resolve the promise for this upload
            resetPaginationAndLoadFiles(); // Refresh file list
        } catch (error) {
            console.error(`FilelistCtrl - Errore durante finalizzazione upload ${uploadState.file.name}:`, error);
            uploadState.reject(error); // Reject the promise
        } finally {
            uploadState.isUploading = false;
            ongoingUploadsMap.delete(uploadId);
        }
    }

    window.cancelUploadFile = async (uploadId) => {
        const uploadState = ongoingUploadsMap.get(uploadId);
        if (!uploadState || !uploadState.isUploading) {
            console.warn(`FilelistCtrl - Tentativo di annullare upload ${uploadId} non in corso o non trovato.`);
            if(window.updateGlobalUploadProgress) { // Update UI if entry exists but not uploading
                const file = uploadState ? uploadState.file : {name: "Sconosciuto"};
                const filePath = uploadState ? uploadState.filePath : "N/A";
                window.updateGlobalUploadProgress(uploadId, file.name, 0, 'Annullato (stato precedente non attivo).', filePath, true, 'cancelled');
            }
            if(uploadState) ongoingUploadsMap.delete(uploadId); // Clean up if entry exists
            return;
        }
        console.log(`FilelistCtrl - Annullamento upload ID: ${uploadId} (${uploadState.file.name})`);
        uploadState.isUploading = false; // Prevent further processing
        uploadState.activeXHRs.forEach(xhr => xhr.abort());
        uploadState.activeXHRs.clear();
        
        if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, uploadState.file.name, (uploadState.uploadedSize / uploadState.expectedFileSize) * 100, 'Annullamento...', uploadState.filePath);

        try {
            await fetch('/upload', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: new URLSearchParams({
                    storage: uploadState.storageName,
                    path: uploadState.filePath,
                    action: 'cancel'
                })
            });
            notifyAppLogic(`Upload di "${uploadState.file.name}" annullato.`, 'warning', {filename: uploadState.file.name});
            if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, uploadState.file.name, (uploadState.uploadedSize / uploadState.expectedFileSize) * 100, 'Annullato.', uploadState.filePath, true, 'cancelled');
        } catch (error) {
            console.error(`FilelistCtrl - Errore durante annullamento server per ${uploadState.file.name}:`, error);
            notifyAppLogic(`Errore durante annullamento upload di "${uploadState.file.name}".`, 'error', {filename: uploadState.file.name, error: error.message});
             if(window.updateGlobalUploadProgress) window.updateGlobalUploadProgress(uploadId, uploadState.file.name, (uploadState.uploadedSize / uploadState.expectedFileSize) * 100, 'Errore annullamento.', uploadState.filePath, true, 'failed');
        } finally {
            uploadState.reject(new Error('Upload annullato dall\'utente.')); // Reject the promise
            ongoingUploadsMap.delete(uploadId); // Clean up
            // Non ricaricare la lista file qui, l'utente potrebbe voler vedere lo stato parziale
        }
    };

    // Exposed to app_logic.js for creating directories
    window.requestCreateDirectory = (storageName, dirPath, folderName) => {
        const newDirPath = dirPath === '' ? folderName : `${dirPath}/${folderName}`;
        if (window.sendMessage) {
            window.sendMessage({
                type: 'create_directory',
                payload: { storage_name: storageName, dir_path: newDirPath }
            });
            notifyAppLogic(`Richiesta creazione cartella "${folderName}"...`, 'info', {filename: folderName});
        }
    };

    // Exposed to app_logic.js for deleting items
    window.requestDeleteItem = (storageName, itemPath, itemName) => {
         if (window.sendMessage) {
            window.sendMessage({
                type: 'delete_item',
                payload: { storage_name: storageName, item_path: itemPath }
            });
            notifyAppLogic(`Richiesta eliminazione "${itemName}"...`, 'info', {filename: itemName});
        }
    };
    
    // Ensure DOM elements are available before adding event listeners
    document.addEventListener('DOMContentLoaded', () => {
        // Re-check elements and add listeners if they were not available initially
        // This is a fallback, ideally elements are found on first pass
        if(triggerUploadBtn && !triggerUploadBtn.onclick) { // Check if listener already attached
             triggerUploadBtn.addEventListener('click', () => { /* ... */ });
        }
        // Similar checks for other buttons if needed
    });


    console.log('filelist_controller.js loaded');
})();
