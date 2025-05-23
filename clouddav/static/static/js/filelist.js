// filelist.js
// Gestisce la logica della lista file nell'iframe centrale, l'upload file tramite HTTP, paginazione e filtri.
const filelistTableBody = document.getElementById('filelist-table').querySelector('tbody');
const uploadFileInput = document.getElementById('upload-file-input');
const nameFilterInput = document.getElementById('name-filter');
const timestampFilterInput = document.getElementById('timestamp-filter');
const enableNameFilterCheckbox = document.getElementById('enable-name-filter');
const enableTimestampFilterCheckbox = document.getElementById('enable-timestamp-filter');
const prevPageBtn = document.getElementById('prev-page-btn');
const nextPageBtn = document.getElementById('next-page-btn');
const currentPageSpan = document.getElementById('current-page');
const totalPagesSpan = document.getElementById('total-pages');
const currentPathArea = document.getElementById('current-path-area'); // Riferimento all'area percorso corrente

let lastListDirectoryRequestId = null; // Variabile per tracciare l'ID dell'ultima richiesta list_directory valida

let currentPage = 1; // Pagina corrente (1-based)
let itemsPerPage = 50; // Elementi per pagina (sarà aggiornato dalla configuratoine del backend)
let totalItems = 0; // Totale elementi (prima della paginazione)
let nameFilter = ''; // Filtro per nome (regex) - applicato solo se checkbox attiva
let timestampFilter = ''; // Filtro per timestamp (stringa RFC3339) - applicato solo se checkbox attiva

// Variabili per tenere traccia dello storage e percorso corrente
let currentStorageName = '';
let currentDirPath = '';

// Mappa per tenere traccia degli upload in corso
// La chiave sarà un ID univoco per ogni upload, il valore conterrà i dettagli dell'upload
// Aggiunto filePath nella mappa per poterlo passare a updateUploadProgress
// Aggiunto resolve/reject per controllare la Promise di uploadChunks
const ongoingUploads = new Map(); // Map<uploadId, { file: File, uploadedSize: number, blockIDs: string[], expectedFileSize: number, chunkSize: number, storageName: string, filePath: string, isUploading: boolean, activeChunkUploads: number, chunkQueue: Array, resolve: Function, reject: Function, activeXHRs: Set<XMLHttpRequest>, clientSHA256: string, parallelChunks: number }>


// Sovrascrive la funzione handleBackendMessage definita nella pagina principale
// Questa funzione viene chiamata dalla pagina principale quando un messaggio è ricevuto dal backend
window.handleBackendMessage = (message) => {
  // console.log('Filelist - Messaggio backend ricevuto:', message); // Log rimosso per ridurre verbosità
  // Controlla il tipo di messaggio ricevuto dal backend
  switch (message.type) {
    case 'list_directory_response':
      // Se la risposta ha un request_id e NON corrisponde all'ultima richiesta valida inviata,
      // significa che è una risposta obsoleta (l'utente ha già cliccato su un'altra cartella).
      // In questo caso, la risposta viene ignorata per evitare "flash" di UI non pertinenti.
      if (message.request_id && message.request_id !== lastListDirectoryRequestId) {
          console.warn(`Filelist - Ignorata risposta list_directory obsoleta (ID: ${message.request_id}). Atteso: ${lastListDirectoryRequestId}`);
          // Assicurati che lo spinner venga nascosto anche per le risposte obsolete
          notifyParentSpinner('hide_loading_spinner');
          return; // Ignora la risposta e esci dalla funzione
      }

      // Se la risposta è valida (corrisponde all'ultima richiesta), procedi con il rendering della lista file.
      if (message.payload && Array.isArray(message.payload.items)) { // Usare message.payload.items (minuscolo) e verificare che sia un array
        renderFileList(message.payload.items); // Renderizza solo gli items della pagina corrente
        totalItems = message.payload.total_items; // Usare message.payload.total_items (minuscolo)
        currentPage = message.payload.page; // Usare message.payload.page (minuscolo)
        itemsPerPage = message.payload.items_per_page; // Usare message.payload.items_per_page (minuscolo)
        updatePaginationControls(); // Aggiorna i controlli de paginazione
      } else {
        console.error('Filelist - Ricevuta list_directory_response con struttura payload non valida:', message.payload); // Log de error mantenido
        // Opzionalmente mostra un messaggio de errore all'utente
        if (window.parent && window.parent.showToast) {
            window.parent.showToast('Errore nel caricare la lista file: struttura dati non valida.', 'error');
        }
        renderFileList([]); // Renderizza una lista vuota per pulire la tabella
        totalItems = 0;
        currentPage = 1;
        updatePaginationControls();
      }
      // Nasconde la rotella di caricamento dopo aver ricevuto e processato la risposta valida
      notifyParentSpinner('hide_loading_spinner');
      break;
    case 'create_directory_response':
      // Se la risposta indica che una directory è stata creata con successo
      console.log('Directory creata con successo:', message.payload); // Log mantenuto
      // Notifica la pagina principale per loggare l'operazione
      notifyFileOperationStatus('create_directory', message.payload.dir_path, 'success', null, message.payload.name); // Passa il nome della cartella
      // Dopo la creazione, ricarica la lista file (torna alla prima pagina con i filtri correnti)
      resetPaginationAndLoadFiles();
      break;
    case 'delete_item_response':
      // Se la risposta indica che un elemento è stato eliminato con successo
      console.log('Elemento eliminato con successo:', message.payload); // Log mantenuto
      // Notifica la pagina principale per loggare l'operazione
      notifyFileOperationStatus('delete_item', message.payload.item_path, 'success', null, message.payload.name); // Passa il nome dell'elemento
      // Dopo l'eliminazione, ricarica la lista file (torna alla prima pagina con i filtri correnti)
      resetPaginationAndLoadFiles();
      break;
    case 'check_directory_contents_response':
            const { has_contents } = message.payload;
            const { storageName, itemPath, itemName, isDirectory } = ongoingDeleteCheck;
            ongoingDeleteCheck = null;
            if (has_contents) {
                if (window.parent && window.parent.postMessage) {
                    window.parent.postMessage({
                        type: 'show_delete_item_modal',
                        payload: {
                            storageName: storageName,
                            itemPath: itemPath,
                            itemName: itemName,
                            warningMessage: `Questa cartella contiene elementi. Sei sicuro di voler eliminare "${itemName}" e tutto il suo contenuto? Questa azione non può essere annullata.`
                        }
                    }, '*');
                }
            } else {
                if (window.parent && window.parent.postMessage) {
                    window.parent.postMessage({
                        type: 'show_delete_item_modal',
                        payload: {
                            storageName: storageName,
                            itemPath: itemPath,
                            itemName: itemName,
                            warningMessage: `Sei sicuro di voler eliminare "${itemName}"? Questa azione non può essere annullata.`
                        }
                    }, '*');
                }
            }
            break;
    case 'error':
      console.error('Filelist - Errore dal backend:', message.payload.error);
      // Utilizza i toast per le notifiche di errore non bloccanti
      if (window.parent && window.parent.showToast) {
        window.parent.showToast('Errore: ' + message.payload.error, 'error');
      }
      // Notifica lo stato dell'operazione per la cronologia messaggi
      notifyFileOperationStatus('backend_error', null, 'error', message.payload.error); // Mantieni per la cronologia
      // Assicurati di nascondere lo spinner in caso di errore
      notifyParentSpinner('hide_loading_spinner');
      break;
  }
};

let ongoingDeleteCheck = null;

function notifyFileOperationStatus(operation, itemPath, status, error = null, filename = null) {
    if (window.parent && window.parent.postMessage) {
        window.parent.postMessage({
            type: 'file_operation_status',
            payload: {
                operation: operation,
                itemPath: itemPath,
                status: status,
                error: error,
                filename: filename // Passa il nome del file per messaggi più specifici
            }
        }, '*');
    }
    // Usa showToast per notifiche all'utente (non bloccanti)
    if (window.parent && window.parent.showToast) {
        let toastType = 'info';
        let message = `Operazione ${operation} su ${itemPath} ${status}.`;
        if (status === 'success') {
            toastType = 'success';
            message = `Operazione ${operation} su "${filename || itemPath}" completata con successo.`;
        } else if (status === 'error') {
            toastType = 'error';
            message = `Errore in ${operation} su "${filename || itemPath}": ${error}`;
        } else if (status === 'warning') {
            toastType = 'warning';
            message = `Attenzione in ${operation} su "${filename || itemPath}": ${error}`;
        }
        window.parent.showToast(message, toastType);
    }

    // Continua ad aggiungere alla cronologia messaggi per un log dettagliato
    if (window.parent && window.parent.addMessageToHistory) {
        window.parent.addMessageToHistory(`Filelist: Operazione ${operation} su ${itemPath} ${status}. ${error ? 'Errore: ' + error : ''}`, status);
    } else {
        console.warn('Filelist - window.parent.addMessageToHistory non disponibile.');
    }

}

function notifyParentSpinner(action) {
    if (window.parent && window.parent.postMessage) {
        window.parent.postMessage({
            type: action
        }, '*');
    }
}

function loadFilelist(storageName, dirPath) {
  currentStorageName = storageName;
  currentDirPath = dirPath;

  updateCurrentPathDisplay();

  // Invia il messaggio alla pagina padre per mostrare lo spinner specifico della lista file
  notifyParentSpinner('show_loading_spinner');

  if (window.parent && window.parent.sendMessage) {
    // Salva l'ID della richiesta quando viene inviata
    lastListDirectoryRequestId = window.parent.sendMessage({ // Modificato: assegna l'ID
      type: 'list_directory',
      payload: {
        storage_name: storageName,
        dir_path: dirPath,
        page: currentPage,
        items_per_page: itemsPerPage,
        name_filter: nameFilter,
        timestamp_filter: timestampFilter
      }
    });
  } else {
    console.error('Filelist - window.parent.sendMessage non disponibile.');
    // Nascondi lo spinner anche in caso di errore nell'invio del messaggio
    notifyParentSpinner('hide_loading_spinner');
  }
}


function updateCurrentPathDisplay() {
  const displayPath = currentDirPath === '' ? '/' : '/' + currentDirPath;
  currentPathArea.textContent = `Percorso Corrente: ${currentStorageName}${displayPath}`;
}

function resetPaginationAndLoadFiles() {
  currentPage = 1;
  loadFilelist(currentStorageName, currentDirPath);
}

function renderFileList(items) {
  filelistTableBody.innerHTML = '';
  // Aggiungi l'elemento ".." per risalire alla directory superiore, se non è la radice dello storage corrente
  if (currentDirPath !== '') {
    const tr = document.createElement('tr');
    const nameTd = document.createElement('td');
    nameTd.textContent = '..';
    nameTd.style.fontWeight = 'bold';
    nameTd.style.cursor = 'pointer';
    nameTd.colSpan = 5; // Occupa tutte le colonne
    nameTd.addEventListener('click', () => {
      // Risali alla directory padre
      const parentPath = currentDirPath.substring(0, currentDirPath.lastIndexOf('/'));
      currentPage = 1; // Resetta la paginazione
      loadFilelist(currentStorageName, parentPath);
    });
    tr.appendChild(nameTd);
    filelistTableBody.appendChild(tr);
  }

  // Itera sugli elementi e aggiungili alla tabella
  items.forEach(item => {
    const tr = document.createElement('tr');

    // Colonna Nome
    const nameTd = document.createElement('td');
    nameTd.textContent = item.name;
    if (item.is_dir) {
      // Se è una directory, rendi il nome cliccabile e in grassetto
      nameTd.style.fontWeight = 'bold';
      nameTd.style.cursor = 'pointer';
      nameTd.addEventListener('click', () => {
        const newDirPath = item.path; // Il percorso dell'item è già completo dal backend
        currentPage = 1; // Resetta la paginazione per la nuova directory
        loadFilelist(currentStorageName, newDirPath);
      });
    }
    tr.appendChild(nameTd);

    // Colonna Tipo
    const typeTd = document.createElement('td');
    typeTd.textContent = item.is_dir ? 'Directory' : 'File';
    tr.appendChild(typeTd);

    // Colonna Dimensione
    const sizeTd = document.createElement('td');
    sizeTd.textContent = item.is_dir ? '' : formatBytes(item.size); // La dimensione è solo per i file
    tr.appendChild(sizeTd);

    // Colonna Modificato
    const modTimeTd = document.createElement('td');
    const modDate = new Date(item.mod_time);
    modTimeTd.textContent = isNaN(modDate.getTime()) ? '' : modDate.toLocaleString(); // Formatta la data
    tr.appendChild(modTimeTd);

    // Colonna Azioni
    const actionsTd = document.createElement('td');
    actionsTd.classList.add('file-actions'); // Aggiungi una classe per lo stile dei pulsanti

    const itemFullPath = item.path; // Il percorso completo dell'item

    // Pulsante Download (solo per i file)
    if (!item.is_dir) {
      const downloadBtn = document.createElement('button');
      downloadBtn.textContent = 'Download';
      downloadBtn.addEventListener('click', () => downloadFile(currentStorageName, itemFullPath));
      actionsTd.appendChild(downloadBtn);
    }

    // Pulsante Elimina (per entrambi i tipi)
    const deleteBtn = document.createElement('button');
    deleteBtn.textContent = 'Elimina';
    deleteBtn.style.backgroundColor = '#f44336'; // Stile specifico per il pulsante Elimina
    deleteBtn.style.color = 'white';
    deleteBtn.addEventListener('click', () => {
        // Logica di conferma eliminazione
        if (item.is_dir) {
            // Se è una directory, prima di eliminare, controlla se ha contenuti
            ongoingDeleteCheck = {
                storageName: currentStorageName,
                itemPath: itemFullPath,
                itemName: item.name,
                isDirectory: true
            };
            if (window.parent && window.parent.sendMessage) {
                window.parent.sendMessage({
                    type: 'check_directory_contents_request',
                    payload: {
                        storage_name: currentStorageName,
                        dir_path: itemFullPath
                    }
                });
            }
        } else {
            // Se è un file, mostra direttamente la modale di conferma
            if (window.parent && window.parent.postMessage) {
                window.parent.postMessage({
                    type: 'show_delete_item_modal',
                    payload: {
                        storageName: currentStorageName,
                        itemPath: itemFullPath,
                        itemName: item.name,
                        warningMessage: `Sei sicuro di voler eliminare "${item.name}"? Questa azione non può essere annullata.`
                    }
                }, '*');
            }
        }
    });
    actionsTd.appendChild(deleteBtn);

    tr.appendChild(actionsTd); // Aggiungi le azioni alla riga
    filelistTableBody.appendChild(tr); // Aggiungi la riga al corpo della tabella
  });
}

function updatePaginationControls() {
  const totalPages = Math.ceil(totalItems / itemsPerPage);
  currentPageSpan.textContent = currentPage;
  totalPagesSpan.textContent = totalPages;
  prevPageBtn.disabled = currentPage <= 1;
  nextPageBtn.disabled = currentPage >= totalPages;
}

function prevPage() {
  if (currentPage > 1) {
    currentPage--;
    loadFilelist(currentStorageName, currentDirPath);
  }
}

function nextPage() {
  const totalPages = Math.ceil(totalItems / itemsPerPage);
  if (currentPage < totalPages) {
    currentPage++;
    loadFilelist(currentStorageName, currentDirPath);
  }
}

function applyFilters() {
  if (enableNameFilterCheckbox.checked) {
    nameFilter = nameFilterInput.value;
  } else {
    nameFilter = '';
  }

  if (enableTimestampFilterCheckbox.checked) {
    const timestampValue = timestampFilterInput.value;
    if (timestampValue) {
      try {
        const date = new Date(timestampValue);
        if (!isNaN(date.getTime())) {
          timestampFilter = date.toISOString();
        } else {
          console.error("Valore data/ora non valido per il filtro timestamp:", timestampValue);
          timestampFilter = '';
          alert("Formato data/ora non valido per il filtro timestamp.");
        }
      } catch (e) {
        console.error("Errore nella lettura del filtro timestamp:", e);
        timestampFilter = '';
      }
    } else {
      timestampFilter = '';
    }
  } else {
    timestampFilter = '';
  }

  resetPaginationAndLoadFiles();
}

function clearFilters() {
  nameFilterInput.value = '';
  timestampFilterInput.value = '';
  enableNameFilterCheckbox.checked = false;
  enableTimestampFilterCheckbox.checked = false;
  nameFilter = '';
  timestampFilter = '';
  resetPaginationAndLoadFiles();
}

function formatBytes(bytes, decimals = 2) {
  if (bytes === 0) return '0 Bytes';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];

  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
}

function downloadFile(storageName, filePath) {
  console.log(`Richiesta download: ${storageName}/${filePath}`);
  const downloadUrl = `/download?storage=${encodeURIComponent(storageName)}&path=${encodeURIComponent(filePath)}`;
  window.open(downloadUrl, '_blank');
  notifyFileOperationStatus('download', `${storageName}/${filePath}`, 'info', null, filePath.split('/').pop()); // Passa il nome del file per il toast
}

async function uploadFile() {
    const files = uploadFileInput.files;
    if (files.length === 0) {
        alert('Seleziona uno o più file da caricare.');
        return;
    }

    if (currentStorageName === '' || currentDirPath === undefined || currentDirPath === null) {
        alert('Seleziona una cartella di destinazione nel treeview.');
        return;
    }

    if (window.parent && window.parent.postMessage) {
        console.log('Filelist - Sending show_chunk_size_modal message to parent.');
        window.parent.postMessage({
            type: 'show_chunk_size_modal',
            payload: { files: Array.from(files) }
        }, '*');
    }
}

async function calculateSHA256(file) {
    if (!file) return null;
    try {
        const buffer = await file.arrayBuffer();
        const hashBuffer = await crypto.subtle.digest('SHA-256', buffer);
        const hashArray = Array.from(new Uint8Array(hashBuffer));
        const hexHash = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
        console.log(`SHA256 calcolato per ${file.name}: ${hexHash}`);
        return hexHash;
    } catch (error) {
        console.error(`Errore nel calcolo SHA256 per ${file.name}:`, error);
        return null;
    }
}

async function startUploadProcess(uploadId, file, chunkSize, parallelChunks) {
    if (currentStorageName === '' || currentDirPath === undefined || currentDirPath === null) {
        console.error(`Filelist - Destinazione upload non valida per ID ${uploadId}.`);
        notifyUploadFailed(uploadId, file.name, 0, 'Destinazione upload non valida.', file.name);
        return;
    }

    const filePath = currentDirPath === '' ? file.name : currentDirPath + '/' + file.name;
    const expectedFileSize = file.size;

    notifyUploadProgress(uploadId, file.name, 0, 'Calcolo SHA256...', filePath);
    const clientSHA256 = await calculateSHA256(file);
    if (!clientSHA256) {
        notifyUploadFailed(uploadId, file.name, 0, 'Fallito calcolo SHA256 lato client.', file.name);
        return;
    }
    notifyUploadProgress(uploadId, file.name, 0, 'SHA256 calcolato. Preparazione...', filePath);

    let uploadPromiseResolve;
    let uploadPromiseReject;
    const uploadPromise = new Promise((resolve, reject) => {
        uploadPromiseResolve = resolve;
        uploadPromiseReject = reject;
    });
    ongoingUploads.set(uploadId, {
        file: file,
        uploadedSize: 0,
        blockIDs: [],
        expectedFileSize: expectedFileSize,
        chunkSize: chunkSize,
        storageName: currentStorageName,
        filePath: filePath,
        isUploading: true,
        activeChunkUploads: 0,
        chunkQueue: [],
        resolve: uploadPromiseResolve,
        reject: uploadPromiseReject,
        activeXHRs: new Set(),
        clientSHA256: clientSHA256,
        parallelChunks: parallelChunks
    });
    try {
        const initiateResponse = await fetch('/upload', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/x-www-form-urlencoded',
            },
            body: new URLSearchParams({
                storage: currentStorageName,
                path: filePath,
                action: 'initiate',
                total_file_size: file.size,
                chunk_size: chunkSize
            })
        });
        if (!initiateResponse.ok) {
            const errorText = await initiateResponse.text();
            throw new Error(`Errore nell'iniziare l'upload: ${initiateResponse.status} - ${errorText}`);
        }

        const data = await initiateResponse.json();
        const uploadState = ongoingUploads.get(uploadId);
        if (!uploadState || !uploadState.isUploading) {
            console.log(`Upload ID ${uploadId} annullato durante l'inizializzazione.`);
            return;
        }

        uploadState.uploadedSize = data.uploaded_size || 0;
        console.log(`Upload ID ${uploadId} iniziato. Dimensione già caricata: ${uploadState.uploadedSize} bytes.`);

        const initialPercentage = (uploadState.uploadedSize / uploadState.expectedFileSize) * 100;
        notifyUploadProgress(uploadId, file.name, initialPercentage, uploadState.uploadedSize > 0 ? 'Ripresa upload...' : 'Caricamento...', filePath);

        let currentOffset = uploadState.uploadedSize;
        let currentChunkIndex = Math.floor(uploadState.uploadedSize / chunkSize);
        while (currentOffset < file.size) {
            const chunk = file.slice(currentOffset, currentOffset + chunkSize);
            const blockID = btoa(String(currentChunkIndex).padStart(20, '0'));
            uploadState.chunkQueue.push({ chunk, blockID, index: currentChunkIndex });
            currentOffset += chunkSize;
            currentChunkIndex++;
        }
        console.log(`Filelist - DEBUG: chunkQueue populated. Length: ${uploadState.chunkQueue.length}. First chunk index: ${uploadState.chunkQueue[0]?.index}`);

        console.log(`Filelist - Avvio invio chunk per ID ${uploadId}. Tentativo di inviare ${uploadState.parallelChunks} chunk in parallelo.`);
        for (let i = 0; i < uploadState.parallelChunks; i++) {
            try {
                console.log(`Filelist - DEBUG: Schedulazione processNextChunk per ID ${uploadId} (loop ${i}).`);
                setTimeout(() => {
                    console.log(`Filelist - DEBUG: Esecuzione callback setTimeout per processNextChunk per ID ${uploadId}.`);
                    const currentUploadState = ongoingUploads.get(uploadId);
                    if (!currentUploadState) {
                        console.error(`Filelist - ERRORE: uploadState non trovato in callback setTimeout per ID ${uploadId}.`);
                        return;
                    }
                    console.log(`Filelist - DEBUG: Stato in callback setTimeout: Active=${currentUploadState.activeChunkUploads}, Queue=${currentUploadState.chunkQueue.length}`);
                    processNextChunk(uploadId);
                }, 0);
            } catch (e) {
                console.error(`Filelist - ERRORE DURANTE SCHEDULAZIONE processNextChunk per ID ${uploadId} (loop ${i}):`, e);
            }
        }
        // await uploadPromise; // QUESTA RIGA È STATA TEMPORANEAMENTE COMMENTATA PER IL DEBUGGING
    } catch (error) {
        console.error(`Errore critico durante l'upload ID ${uploadId} in startUploadProcess:`, error);
        const uploadState = ongoingUploads.get(uploadId);
        if (uploadState && uploadState.isUploading) {
            notifyUploadFailed(uploadId, file.name, uploadState.uploadedSize, error.message, file.name);
            uploadState.isUploading = false;
            ongoingUploads.delete(uploadId);
            loadFilelist(currentStorageName, currentDirPath);
        } else if (uploadState) {
            console.log(`Upload ID ${uploadId} già annullato dall'utente, non invio notifica di fallimento.`);
        }
    }
}


async function processNextChunk(uploadId) {
    try {
        console.log(`Filelist - DEBUG: processNextChunk invoked for upload ID: ${uploadId}. Active: ${ongoingUploads.get(uploadId)?.activeChunkUploads}, Queue: ${ongoingUploads.get(uploadId)?.chunkQueue.length}`);
        const uploadState = ongoingUploads.get(uploadId);
        if (!uploadState || !uploadState.isUploading) {
            console.log(`Filelist - Upload ID ${uploadId} annullato o stato non valido, non processa più chunk.`);
            return;
        }

        // Se non ci sono chunk in coda E non ci sono chunk attivi, finalizza
        if (uploadState.chunkQueue.length === 0 && uploadState.activeChunkUploads === 0) {
            console.log(`Filelist - Tutti i chunk processati per ID ${uploadId}. Inizio finalizzazione.`);
            finalizeUpload(uploadId);
            return;
        }

        // Se ci sono troppi upload attivi o la coda è vuota, non fare nulla per ora.
        // Questa funzione verrà richiamata quando un chunk attivo si completa.
        if (uploadState.activeChunkUploads >= uploadState.parallelChunks || uploadState.chunkQueue.length === 0) {
            console.log(`Filelist - DEBUG: processNextChunk waiting for active uploads to finish or queue to have more chunks. Active: ${uploadState.activeChunkUploads}, Parallel: ${uploadState.parallelChunks}, Queue: ${uploadState.chunkQueue.length}.`);
            return;
        }

        const nextChunkData = uploadState.chunkQueue.shift();
        if (!nextChunkData) {
            console.warn(`Filelist - nextChunkData is undefined for upload ID: ${uploadId}. Queue might be empty unexpectedly after shift.`);
            return;
        }
        
        console.log(`Filelist - DEBUG: Preparing to send chunk Index: ${nextChunkData.index} for upload ID: ${uploadId}`);
        console.log(`Filelist - DEBUG: BEFORE activeChunkUploads increment: ${uploadState.activeChunkUploads}`);
        uploadState.activeChunkUploads++;
        console.log(`Filelist - DEBUG: AFTER activeChunkUploads increment: ${uploadState.activeChunkUploads}`);

        const { chunk, blockID, index: currentChunkIndex } = nextChunkData;
        const { file, expectedFileSize, storageName, filePath, chunkSize } = uploadState;
        const formData = new FormData();
        formData.append('storage', storageName);
        formData.append('path', filePath);
        formData.append('action', 'chunk');
        formData.append('block_id', blockID);
        formData.append('chunk_index', currentChunkIndex);
        formData.append('chunk_size', chunkSize);
        formData.append('chunk', chunk);
        const xhr = new XMLHttpRequest();
        uploadState.activeXHRs.add(xhr);

        xhr.open('POST', '/upload', true);
        xhr.timeout = 600000;
        if (xhr.upload) {
            xhr.upload.onprogress = (event) => {
                if (event.lengthComputable) {
                    const currentChunkProgress = event.loaded;
                    const totalUploaded = uploadState.uploadedSize + currentChunkProgress;
                    const percentage = (totalUploaded / expectedFileSize) * 100;
                    notifyUploadProgress(uploadId, file.name, percentage, `Caricamento chunk ${currentChunkIndex + 1}...`, filePath);
                }
            };
        }

        xhr.onload = () => {
            console.log(`Filelist - DEBUG: XHR onload for chunk ${currentChunkIndex}, ID ${uploadId}. Status: ${xhr.status}`);
            uploadState.activeXHRs.delete(xhr);
            uploadState.activeChunkUploads--;
            console.log(`Filelist - DEBUG: AFTER XHR onload activeChunkUploads decrement: ${uploadState.activeChunkUploads}`);

            if (xhr.status >= 200 && xhr.status < 300) {
                if (uploadState.isUploading) {
                    uploadState.uploadedSize += chunk.size;
                    uploadState.blockIDs.push(blockID);
                    console.log(`Filelist - Chunk caricato per ID ${uploadId} (Index: ${currentChunkIndex}, Size: ${formatBytes(chunk.size)}). Dimensione totale caricata: ${formatBytes(uploadState.uploadedSize)}`);
                    const percentage = (uploadState.uploadedSize / expectedFileSize) * 100;
                    notifyUploadProgress(uploadId, file.name, percentage, `Chunk ${currentChunkIndex + 1} caricato.`, filePath);
                }
                // Call processNextChunk again to send the next chunk
                processNextChunk(uploadId);
            } else {
                const errorText = xhr.responseText || `Status: ${xhr.status}`;
                console.error(`Filelist - Errore nel caricare il chunk per ID ${uploadId} (Index: ${currentChunkIndex}): ${errorText}`);
                if (uploadState.isUploading) {
                    uploadState.reject(new Error(`Errore nel caricare il chunk: ${errorText}`));
                }
                // Call processNextChunk again to attempt to send the next chunk (or re-evaluate)
                processNextChunk(uploadId);
            }
        };

        xhr.onerror = () => {
            console.log(`Filelist - DEBUG: XHR onerror for chunk ${currentChunkIndex}, ID ${uploadId}.`);
            uploadState.activeXHRs.delete(xhr);
            uploadState.activeChunkUploads--;
            console.log(`Filelist - DEBUG: AFTER XHR onerror activeChunkUploads decrement: ${uploadState.activeChunkUploads}`);
            console.error(`Filelist - Errore di rete durante il caricamento del chunk per ID ${uploadId} (Index: ${currentChunkIndex}, Size: ${formatBytes(chunk.size)}).`);
            if (uploadState.isUploading) {
                uploadState.reject(new Error('Errore di rete durante il caricamento del chunk.'));
            }
            // Call processNextChunk again to attempt to send the next chunk (or re-evaluate)
            processNextChunk(uploadId);
        };

        xhr.onabort = () => {
            console.log(`Filelist - DEBUG: XHR onabort for chunk ${currentChunkIndex}, ID ${uploadId}.`);
            uploadState.activeXHRs.delete(xhr);
            uploadState.activeChunkUploads--;
            console.log(`Filelist - DEBUG: AFTER XHR onabort activeChunkUploads decrement: ${uploadState.activeChunkUploads}`);
            console.log(`Filelist - Upload chunk interrotto per ID ${uploadId} (Index: ${currentChunkIndex}).`);
            // Call processNextChunk to potentially send another chunk if a slot opened up
            processNextChunk(uploadId);
        };

        xhr.ontimeout = () => {
            console.log(`Filelist - DEBUG: XHR ontimeout for chunk ${currentChunkIndex}, ID ${uploadId}.`);
            uploadState.activeXHRs.delete(xhr);
            uploadState.activeChunkUploads--;
            console.log(`Filelist - DEBUG: AFTER XHR ontimeout activeChunkUploads decrement: ${uploadState.activeChunkUploads}`);
            console.error(`Filelist - Timeout durante il caricamento del chunk per ID ${uploadId} (Index: ${currentChunkIndex}, Size: ${formatBytes(chunk.size)}).`);
            if (uploadState.isUploading) {
                uploadState.reject(new Error('Timeout durante il caricamento del chunk.'));
            }
            // Call processNextChunk again as a slot is now free
            processNextChunk(uploadId);
        };

        console.log(`Filelist - DEBUG: Sending XHR for chunk ${currentChunkIndex}, ID ${uploadId}.`);
        xhr.send(formData);
        console.log(`Filelist - DEBUG: XHR sent for chunk ${currentChunkIndex}, ID ${uploadId}.`);
    } catch (e) {
        console.error(`Filelist - ERRORE CRITICO in processNextChunk per upload ID ${uploadId}:`, e);
        const uploadState = ongoingUploads.get(uploadId);
        if (uploadState && uploadState.isUploading) {
            uploadState.reject(new Error(`Errore interno durante l'elaborazione del chunk: ${e.message}`));
        }
    }
}


async function finalizeUpload(uploadId) {
    const uploadState = ongoingUploads.get(uploadId);
    if (!uploadState || !uploadState.isUploading) {
        console.log(`Filelist - Upload ID ${uploadId} annullato o stato non valido, non finalizza.`);
        return;
    }

    const { file, expectedFileSize, storageName, filePath, clientSHA256 } = uploadState;
    if (uploadState.uploadedSize < file.size) {
        console.warn(`Filelist - Tentativo di finalizzare l'upload ID ${uploadId} ma non tutti i chunk sono stati caricati. uploadedSize: ${uploadState.uploadedSize}, file.size: ${file.size}`);
        if (uploadState.reject) {
            uploadState.reject(new Error("Finalizzazione fallita: non tutti i chunk sono stati caricati."));
        }
        return;
    }

    console.log(`Filelist - Invio richiesta finalizzazione upload per ID ${uploadId}...`);
    notifyUploadProgress(uploadId, file.name, 100, 'Finalizzazione e verifica...', filePath);

    try {
        const finalizeResponse = await fetch('/upload', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/x-www-form-urlencoded',
            },
            body: new URLSearchParams({
                storage: storageName,
                path: filePath,
                action: 'finalize',
                block_ids: JSON.stringify(uploadState.blockIDs),
                client_sha256: clientSHA256,
                total_file_size: expectedFileSize
            })
        });
        console.log(`Filelist - Risposta finalizzazione ricevuta per ID ${uploadId}. Status:`, finalizeResponse.status);
        if (!finalizeResponse.ok) {
            const errorText = await finalizeResponse.text();
            console.error(`Filelist - Errore nel finalizzare l'upload per ID ${uploadId}: ${finalizeResponse.status} - ${errorText}`);
            if (uploadState.reject) {
                 uploadState.reject(new Error(`Errore nel finalizzare l'upload: ${finalizeResponse.status} - ${errorText}`));
            }
            return;
        }

        console.log(`Filelist - Upload ID ${uploadId} completato con successo!`);
        notifyUploadComplete(uploadId, file.name, filePath);
        if (uploadState.resolve) {
            uploadState.resolve();
        }
    } catch (error) {
        console.error(`Filelist - Errore durante la finalizzazione dell'upload per ID ${uploadId} (catch):`, error);
        notifyUploadFailed(uploadId, file.name, uploadState.uploadedSize, error.message, filePath);
        if (uploadState.reject) {
            uploadState.reject(error);
        }
    }
}

async function cancelUpload(uploadId) {
    const uploadState = ongoingUploads.get(uploadId);
    if (!uploadState) {
        console.warn(`Filelist - Richiesta di annullamento per upload ID ${uploadId}, ma non è in corso (stato non trovato).`);
        return;
    }
    if (!uploadState.isUploading) {
        console.warn(`Filelist - Richiesta di annullamento per upload ID ${uploadId}, ma non è in corso (isUploading è false).`);
        return;
    }

    console.log(`Filelist - Tentativo di annullare l'upload per ID ${uploadId} (${uploadState.file.name})`);
    uploadState.isUploading = false;
    uploadState.activeXHRs.forEach(xhr => {
        if (xhr.readyState !== XMLHttpRequest.UNSENT && xhr.readyState !== XMLHttpRequest.DONE) {
            console.log(`Filelist - Annullamento richiesta XHR attiva per ID ${uploadId}.`);
            xhr.abort();
        }
    });
    uploadState.activeXHRs.clear();

    notifyUploadCancelled(uploadId, uploadState.file.name, uploadState.uploadedSize, uploadState.filePath);

    await cancelUploadServer(uploadId);

    ongoingUploads.delete(uploadId);
    loadFilelist(currentStorageName, currentDirPath);
    if (uploadState.reject) {
        uploadState.reject(new Error("Upload annullato dall'utente."));
    }
}

async function cancelUploadServer(uploadId) {
     const uploadState = ongoingUploads.get(uploadId);
     if (!uploadState) {
         console.error(`Filelist - Stato upload non trovato per ID ${uploadId} durante l'annullamento lato server.`);
         return;
     }

     const { storageName, filePath } = uploadState;
     try {
         console.log(`Filelist - Invio richiesta cancel upload al server per ID ${uploadId}...`);
         const cancelResponse = await fetch('/upload', {
             method: 'POST',
             headers: {
                 'Content-Type': 'application/x-www-form-urlencoded',
             },
             body: new URLSearchParams({
                 storage: storageName,
                 path: filePath,
                 action: 'cancel'
             })
         });
         console.log(`Filelist - Risposta cancel ricevuta per ID ${uploadId}. Status:`, cancelResponse.status);
         if (!cancelResponse.ok) {
             const errorText = await cancelResponse.text();
             console.error(`Filelist - Erro nel cancellare l'upload lato server per ID ${uploadId}: ${cancelResponse.status} - ${errorText}`);
         } else {
             console.log(`Filelist - Upload ID ${uploadId} cancellato con successo lato server.`);
         }
     } catch (error) {
         console.error(`Filelist - Erro de rete durante la cancellazione dell\'upload lato server per ID ${uploadId}:`, error);
     }
}

function createNewFolderConfirmed(storageName, dirPath, folderName) {
  console.log(`Richiesta creazione nuova cartella: ${storageName}/${dirPath}/${folderName}`);
  const newDirPath = dirPath === '' ? folderName : dirPath + '/' + folderName;
  if (window.parent && window.parent.sendMessage) {
    window.parent.sendMessage({
      type: 'create_directory',
      payload: {
        storage_name: storageName,
        dir_path: newDirPath
      }
    });
    notifyFileOperationStatus('create_directory', `${storageName}/${newDirPath}`, 'info', null, folderName); // Passa il nome della cartella
  } else {
    console.error('Filelist - window.parent.sendMessage non disponibile per inviare create_directory.');
  }
}

function deleteItemConfirmed(storageName, itemPath, itemName) {
  console.log(`Richiesta eliminazione elemento: ${storageName}/${itemPath}`);
  if (window.parent && window.parent.sendMessage) {
    window.parent.sendMessage({
      type: 'delete_item',
      payload: {
        storage_name: storageName,
        item_path: itemPath
      }
    });
    notifyFileOperationStatus('delete_item', `${storageName}/${itemPath}`, 'info', null, itemName); // Passa il nome dell'elemento
  } else {
    console.error('Filelist - window.parent.sendMessage non disponibile per inviare delete_item.');
  }
}

function notifyUploadProgress(uploadId, fileName, percentage, statusText, filePath) {
    if (window.parent && window.parent.postMessage) {
        window.parent.postMessage({
            type: 'upload_progress',
            payload: { uploadId, fileName, percentage, statusText, filePath }
        }, '*');
    }
}

function notifyUploadComplete(uploadId, fileName, filePath) {
     if (window.parent && window.parent.postMessage) {
        window.parent.postMessage({
            type: 'upload_complete',
            payload: { uploadId, fileName, filePath }
        }, '*');
     }
}

function notifyUploadFailed(uploadId, fileName, uploadedSize, error, filePath) {
     if (window.parent && window.parent.postMessage) {
        const uploadState = ongoingUploads.get(uploadId);
        const expectedFileSize = uploadState ? uploadState.expectedFileSize : 1;
        const percentage = (uploadedSize / expectedFileSize) * 100;
        window.parent.postMessage({
            type: 'upload_failed',
            payload: { uploadId, fileName, percentage, error, filePath }
        }, '*');
     }
}

function notifyUploadCancelled(uploadId, fileName, uploadedSize, filePath) {
     if (window.parent && window.parent.postMessage) {
         const uploadState = ongoingUploads.get(uploadId);
         const expectedFileSize = uploadState ? uploadState.expectedFileSize : 1;
         const percentage = (uploadedSize / expectedFileSize) * 100;
         window.parent.postMessage({
             type: 'upload_cancelled',
             payload: { uploadId, fileName, percentage, filePath }
         }, '*');
     }
}


window.addEventListener('message', event => {
    if (event.data.type === 'load_filelist') {
        const payload = event.data.payload;
        // Aggiunto controllo per assicurare che le variabili siano inizializzate, utile per caricamente iframe
        if (typeof currentStorageName !== 'undefined' && typeof currentDirPath !== 'undefined' && typeof currentPage !== 'undefined' && typeof itemsPerPage !== 'undefined' && typeof nameFilter !== 'undefined' && typeof timestampFilter !== 'undefined') {
             console.log('Filelist - Variables initialized, loading file list directly.');
             loadFilelist(payload.storageName, payload.dirPath);
        } else {
             console.warn('Filelist - Received load_filelist message before variables are fully initialized. Attempting delayed load.');
             setTimeout(() => {
                 if (typeof currentStorageName !== 'undefined' && typeof currentDirPath !== 'undefined' && typeof currentPage !== 'undefined' && typeof itemsPerPage !== 'undefined' && typeof nameFilter !== 'undefined' && typeof timestampFilter !== 'undefined') {
                     console.log('Filelist - Variables initialized after delay, loading file list.');
                     loadFilelist(payload.storageName, payload.dirPath);
                 } else {
                      console.error('Filelist - Variables still undefined after delay. Cannot load file list.');
                 }
             }, 100);
        }
    } else if (event.data.type === 'cancel_upload_request') {
        const uploadIdToCancel = event.data.payload.uploadId;
        console.log(`Filelist - Received cancel upload request from parent for ID: ${uploadIdToCancel}.`);
        cancelUpload(uploadIdToCancel);
    } else if (event.data.type === 'start_upload_process') {
        const payload = event.data.payload;
        const files = payload.files;
        const chunkSize = payload.chunkSize;
        const parallelChunks = payload.parallelChunks;
        console.log(`Filelist - Received start_upload_process message from parent. Starting upload for ${files.length} files with chunk size ${chunkSize} and ${parallelChunks} parallel chunks.`);
        for (let i = 0; i < files.length; i++) {
            const file = files[i];
            // Genera un ID di upload unico per ciascun file
            const uploadId = Date.now() + '-' + i + '-' + file.name.replace(/[^a-zA-Z0-9]/g, '');
            startUploadProcess(uploadId, file, chunkSize, parallelChunks);
        }
    } else if (event.data.type === 'create_directory_confirmed') {
         const payload = event.data.payload;
         console.log(`Filelist - Received create_directory_confirmed message from parent for storage '${payload.storageName}', path '${payload.dirPath}', folder name '${payload.folderName}'.`);
         createNewFolderConfirmed(payload.storageName, payload.dirPath, payload.folderName);
    } else if (event.data.type === 'delete_item_confirmed') {
         const payload = event.data.payload;
         console.log(`Filelist - Received delete_item_confirmed message from parent for storage '${payload.storageName}', item path '${payload.itemPath}'.`);
         deleteItemConfirmed(payload.storageName, payload.itemPath, payload.itemName);
    }
});