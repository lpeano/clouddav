// treeview.js
// Gestisce la logica del treeview nell'iframe sinistro.

// Riferimento all'elemento radice della lista non ordinata dove verrà costruito il treeview
const treeviewRoot = document.getElementById('treeview-root');
// Mantiene un riferimento all'elemento (li) attualmente selezionato nel treeview
let selectedElement = null; // Mantiene un riferimento all'elemento attualmente selezionato

// Funzione per notificare la pagina principale per aggiungere un messaggio alla cronologia
function notifyParentMessage(message, type = 'info') {
    if (window.parent && window.parent.addMessageToHistory) {
        window.parent.addMessageToHistory(`Treeview: ${message}`, type);
    } else {
        console.warn('Treeview - window.parent.addMessageToHistory non disponibile.');
    }
}


// Sovrascrive la funzione handleBackendMessage definita nella pagina principale
// Questa funzione viene chiamata dalla pagina principale quando un messaggio è ricevuto dal backend
window.handleBackendMessage = (message) => {
    console.log('Treeview - Messaggio backend ricevuto:', message); // Keep this log for debugging

    // Controlla il tipo di messaggio ricevuto dal backend
    switch (message.type) {
        case 'get_filesystems_response': // Questo messaggio ora contiene la lista di StorageConfig
            // console.log('Treeview - Payload get_filesystems_response:', message.payload); // Log rimosso
            // Se la risposta contiene l'elenco dei storage accessibili, li renderizza nel treeview
            renderStorages(message.payload); // Chiamata alla nuova funzione renderStorages
            notifyParentMessage('Elenco storage ricevuto.');
            break;
        case 'list_directory_response':
            // console.log('Treeview - Payload list_directory_response:', message.payload); // Log rimosso

            // Se la risposta contiene il contenuto di una directory, lo renderizza sotto l'elemento richiedente
            // Trova l'elemento nel treeview che ha richiesto l'elenco usando l'ID della richiesta
            // *** Aggiungi un controllo per verificare se message.request_id esiste ***
            if (message.request_id) {
                 const requestingElement = document.querySelector(`[data-request-id="${message.request_id}"]`);
                if (requestingElement) {
                    // console.log('Treeview - Elemento richiedente trovato con ID:', message.request_id, 'Elemento:', requestingElement); // Log rimosso

                    // Accedi all'array 'items' (minuscolo) all'interno del payload
                    if (message.payload && Array.isArray(message.payload.items)) { // Usare message.payload.items (minuscolo) e verificare che sia un array
                         renderDirectoryContent(requestingElement, message.payload.items); // Passa gli items
                         notifyParentMessage(`Contenuto directory "${requestingElement.dataset.path || '/'}" caricato.`);
                    } else {
                         console.error('Treeview - Risposta list_directory_response con payload.items mancante, non valido o non un array:', message.payload); // Log de error mantenuto
                         // Potresti voler mostrare un messaggio de errore all'utente qui
                         notifyParentMessage(`Errore nel caricare contenuto directory: struttura dati non valida.`, 'error');
                    }

                    // Rimuove l'ID della richiesta dall'elemento dopo aver ricevuto la risposta
                    requestingElement.removeAttribute('data-request-id'); // Rimuove l'ID della richiesta
                } else {
                     console.warn("Treeview - Risposta list_directory_response senza elemento richiedente corrispondente per ID:", message.request_id); // Log de avviso mantenido
                     // Questo potrebbe accadere per risposte Long Polling se non gestiamo bene lo stato
                     // Potremmo dover trovare l'elemento basandoci sul percorso invece che sull'ID richiesta.
                     notifyParentMessage(`Avviso: Risposta directory ricevuta senza ID richiesta corrispondente.`, 'warning');
                 }
            } else {
                console.warn("Treeview - Risposta list_directory_response ricevuta senza RequestID."); // Log per risposte senza ID
                notifyParentMessage(`Avviso: Risposta directory ricevuta senza ID richiesta.`, 'warning');
            }
            break;
        case 'error':
            // Gestisce i messaggi de errore dal backend
            console.error('Treeview - Errore dal backend:', message.payload.error); // Log de error mantenido
            alert('Errore: ' + message.payload.error); // Mostra un alert semplice per gli errori
             notifyParentMessage(`Errore dal backend: ${message.payload.error}`, 'error');
            break;
        // Aggiungi altri tipi de messaggi se necessario
    }
};
// Richiede l'elenco dei storage accessibili al backend all'avvio della pagina
document.addEventListener('DOMContentLoaded', () => {
    // console.log('Treeview - DOM content loaded, requesting filesystems.'); // Log rimosso
    notifyParentMessage('Treeview caricato, richiesta elenco storage...');
    // Invia un messaggio al backend tramite la funzione sendMessage della pagina principale
    if (window.parent && window.parent.sendMessage) {
        window.parent.sendMessage({ type: 'get_filesystems' }); // Richiesta per la lista degli storage
    } else {
        console.error('Treeview - window.parent.sendMessage non disponibile.'); // Log de error mantenuto
        notifyParentMessage('Errore: Funzione sendMessage non disponibile nel parent.', 'error');
    }
});
// Renderizza l'elenco dei storage nel treeview
// Questa funzione riceve una lista di StorageConfig dal backend
function renderStorages(storages) {
    // console.log('Treeview - Rendering initial storages:', storages); // Log rimosso

    // Pulisce il contenuto esistente del treeview
    treeviewRoot.innerHTML = '';
    // Aggiungi un controllo per assicurarti che 'storages' sia un array
    if (!Array.isArray(storages)) {
        console.error('Treeview - Argomento storages per rendering iniziale non è un array:', storages); // Log de error mantenuto
        // Potresti voler mostrare un messaggio de errore all'utente qui
        notifyParentMessage('Errore nel rendering storage: dati non validi.', 'error');
        return; // Esci dalla funzione se non è un array
    }


    // Itera sugli elementi (storage configurati) ricevuti
    storages.forEach(storageCfg => {
        // console.log('Treeview - Rendering initial storage:', storageCfg); // Log rimosso
        // Crea un nuovo elemento li per ogni storage
        const li = document.createElement('li');
        li.classList.add('directory'); // Tratta il nodo radice dello storage come una directory
        li.textContent = storageCfg.name; // Imposta il testo dell'elemento con il nome dello storage
        li.dataset.storageName = storageCfg.name; // Memorizza il nome dello storage come data attribute
        li.dataset.path = ''; // Imposta il percorso base dello storage come stringa vuota (root)
        li.dataset.storageType = storageCfg.type; // Memorizza il tipo di storage (opzionale, per icone diverse)

        // Aggiunge un listener per gestire il click sull'elemento
        // Utilizziamo un listener per l'elemento completo per la selezione
        li.addEventListener('click', handleItemSelection);

        // Aggiungiamo un indicatore separato o un listener per espandere/collassare
        // Modifichiamo handleItemClick per gestire entrambe le cose in base al target dell'evento

        // Aggiunge l'elemento li alla radice del treeview
        treeviewRoot.appendChild(li);
        // Aggiungi un ul vuoto per i contenuti che verranno caricati dinamicamente
        const ul = document.createElement('ul');
        li.appendChild(ul);
    });
     // console.log('Treeview - Fine rendering initial storages.'); // Log rimosso
}

// Gestisce la selezione di un elemento del treeview (directory o file)
function handleItemSelection(event) {
     // Impedisce che il click si propaghi agli elementi genitori (per evitare doppie chiamate)
    event.stopPropagation();
    const clickedElement = event.currentTarget; // Usa currentTarget per l'elemento li cliccato

    // Aggiorna la selezione visuale nel treeview
    if (selectedElement) {
        selectedElement.classList.remove('selected'); // Rimuove la classe 'selected' dall'elemento precedentemente selezionato
    }
    clickedElement.classList.add('selected'); // Aggiunge la classe 'selected' all'elemento cliccato
    selectedElement = clickedElement; // Aggiorna l'elemento selezionato

    const storageName = clickedElement.dataset.storageName;
    const itemPath = clickedElement.dataset.path;
    const storageType = clickedElement.dataset.storageType; // Recupera il tipo di storage
    const isDirectory = clickedElement.classList.contains('directory');

    // Notifica la pagina principale (index.html) che un elemento è stato selezionato
     if (window.parent && window.parent.handleTreeviewSelect) {
         window.parent.handleTreeviewSelect(storageName, itemPath, storageType); // Passa storageName, itemPath e storageType
     }

     // Se l'elemento cliccato è una directory E il click è avvenuto sull'elemento LI stesso (non su un figlio)
     // gestisci l'espansione/compressione
     if (isDirectory && event.target === clickedElement) {
         toggleDirectory(clickedElement);
     }
}


// Funzione per espandere/collassare una directory nel treeview
function toggleDirectory(directoryElement) {
    directoryElement.classList.toggle('open'); // Alterna la classe 'open'

    // Se la directory viene aperta e non ha ancora figli nel DOM, richiedi il contenuto
    if (directoryElement.classList.contains('open')) {
        const ul = directoryElement.querySelector('ul');
        if (ul && ul.children.length === 0 && !directoryElement.dataset.requestId) { // Controlla se non ha figli e non c'è una richiesta in corso
            const storageName = directoryElement.dataset.storageName;
            const itemPath = directoryElement.dataset.path;

            // Per il treeview, carichiamo sempre la prima pagina con i filtri de default
            const page = 1;
            const itemsPerPage = 50; // Puoi rendere questo configurabile se necessario
            const nameFilter = '';
            const timestampFilter = '';

            // Richiede l'elenco dei file e sottocartelle al backend
             if (window.parent && window.parent.sendMessage) {
                 const requestID = window.parent.sendMessage({
                    type: 'list_directory',
                    payload: {
                        storage_name: storageName, // Usa storage_name
                        dir_path: itemPath,
                        page: page,
                        items_per_page: itemsPerPage,
                        name_filter: nameFilter,
                        timestamp_filter: timestampFilter
                    }
                 });
                 // Associa l'ID della richiesta all'elemento per poter gestire la risposta specifica
                 directoryElement.dataset.requestId = requestID;
                 notifyParentMessage(`Richiesta contenuto directory per "${itemPath || '/'}"...`);
             } else {
                 console.error('Treeview - window.parent.sendMessage non disponibile per inviare list_directory durante toggle.');
                 notifyParentMessage('Errore: Funzione sendMessage non disponibile nel parent per list_directory.', 'error');
             }
        } else {
             console.log('Directory già caricata o richiesta in corso per:', directoryElement.dataset.path);
             notifyParentMessage(`Directory "${directoryElement.dataset.path || '/'}" già caricata o richiesta in corso.`);
        }
    }
}


// Renderizza il contenuto di una directory nel treeview
// Aggiunge gli elementi (file e directory) sotto l'elemento directory genitore
// Riceve una lista di ItemInfo
function renderDirectoryContent(directoryElement, items) {
    // console.log('Treeview - Inizio rendering contenuto directory per:', directoryElement.dataset.path); // Log rimosso
    // console.log('Treeview - Elemento directory:', directoryElement); // Log rimosso
    // console.log('Treeview - Items da renderizzare:', items); // Log rimosso

    const ul = directoryElement.querySelector('ul'); // Trova la lista figli della directory genitore
    ul.innerHTML = ''; // Pulisce il contenuto esistente della lista figli

    // Aggiungi l'elemento ".." per risalire alla directory superiore, se non è la radice dello storage corrente
    // Add the ".." element to go up to the parent directory, if it's not the root of the current storage
    const currentPath = directoryElement.dataset.path;
    const storageName = directoryElement.dataset.storageName; // Recupera storageName dall'elemento padre
     const storageType = directoryElement.dataset.storageType; // Recupera storageType dall'elemento padre
    // console.log('Treeview - Percorso corrente per ".." check:', currentPath); // Log rimosso
    if (currentPath !== '') { // Non mostrare ".." per la directory radice dello storage
        // console.log('Treeview - Aggiungo elemento ".."'); // Log rimosso
        const parentLi = document.createElement('li');
        parentLi.classList.add('directory'); // Tratta ".." come una directory
        parentLi.textContent = '..';
        parentLi.dataset.storageName = storageName; // Imposta storageName per ".."
        parentLi.dataset.storageType = storageType; // Imposta storageType per ".."
        // Calcola il percorso della directory genitore
        const parentPath = currentPath.substring(0, currentPath.lastIndexOf('/'));
        parentLi.dataset.path = parentPath; // Il percorso genitore è la parte prima dell'ultimo '/'
        parentLi.addEventListener('click', handleItemSelection); // Usa handleItemSelection
        ul.appendChild(parentLi);
    } else {
        // console.log('Treeview - Percorso corrente è radice, non aggiungo "..".'); // Log rimosso
    }


    // Ordina gli elementi: prima le directory, poi i file, entrambi in ordine alfabetico
    // Questo ordinamento è già fatto dal backend, ma lo manteniamo qui per coerenza visiva
    items.sort((a, b) => {
        if (a.is_dir === b.is_dir) {
            return a.name.localeCompare(b.name); // Ordine alfabetico per lo stesso tipo
        }
        return a.is_dir ? -1 : 1; // Directory prima dei file
    });
    // Itera sugli elementi (file/directory/blob) nella directory corrente
    items.forEach(item => {
        // console.log('Treeview - Rendering item:', item.name); // Log rimosso

        // *** AGGIUNGI QUESTO CONTROLLO ***
        if (!item.is_dir) {
            // Salta i file, non li renderizziamo nel treeview
            return;
        }
        // *** FINE CONTROLLO ***


        const li = document.createElement('li'); // Crea un nuovo elemento li per ogni item
        li.textContent = item.name; // Imposta il testo dell'elemento
        // Memorizza il nome dello storage e il percorso completo dell'item come data attributes
        li.dataset.storageName = storageName; // Usa storageName dall'elemento padre
        li.dataset.path = item.path; // Usa il campo 'path' dall'ItemInfo ricevuto dal backend
        li.dataset.storageType = storageType; // Imposta storageType per ".."
        // Aggiunge un listener per gestire il click sull'item
        li.addEventListener('click', handleItemSelection); // Usa handleItemSelection

        // Se l'item è una directory
        li.classList.add('directory'); // Aggiunge la classe 'directory'
        // Aggiungi un ul vuoto per i contenuti che verranno caricati dinamicamente
        const childUl = document.createElement('ul');
        li.appendChild(childUl); // Aggiunge la lista figli all'elemento directory

        // Rimosso il blocco 'else' per i file, che ora vengono saltati.
        // if (item.is_dir) {
        //     // Se l'item è una directory
        //     li.classList.add('directory'); // Aggiunge la classe 'directory'
        //     // Aggiungi un ul vuoto per i contenuti che verranno caricati dinamicamente
        //     const childUl = document.createElement('ul');
        //     li.appendChild(childUl); // Aggiunge la lista figli all'elemento directory
        // } else {
        //     // Se l'item è un file
        //     li.classList.add('file'); // Aggiunge la classe 'file'
        // }

        ul.appendChild(li); // Aggiunge l'elemento item alla lista figli della directory genitore
    });
     // console.log('Treeview - Fine rendering contenuto directory.'); // Log rimosso
}


// Funzione per espandere ricorsivamente tutti i nodi del treeview
function expandAll() {
    notifyParentMessage('Espansione di tutti i nodi del treeview...');
    const allDirectories = treeviewRoot.querySelectorAll('li.directory');
    allDirectories.forEach(directoryElement => {
        if (!directoryElement.classList.contains('open')) {
             toggleDirectory(directoryElement); // Espandi se non è già aperto
        }
    });
}

// Funzione per comprimere ricorsivamente tutti i nodi del treeview
function collapseAll() {
    notifyParentMessage('Compressione di tutti i nodi del treeview...');
    const allDirectories = treeviewRoot.querySelectorAll('li.directory.open'); // Seleziona solo le directory aperte
    // Comprimi partendo dai nodi più profondi (opzionale ma può prevenire glitch visivi)
    // O semplicemente comprimi tutti, l'ordine non è critico per il display: none
    allDirectories.forEach(directoryElement => {
         directoryElement.classList.remove('open'); // Rimuove la classe 'open'
    });
}


// Funzione chiamata dalla pagina principale per selezionare un elemento (opzionale)
// Potrebbe essere utile per sincronizzare la selezione tra i due iframes.
// function selectItem(storageName, itemPath) {
//     // Implementa la logica per trovare e selezionare un elemento nel treeview
// }

// Aggiungi un listener per i messaggi postati dalla pagina principale (index.html)
// Questo è un meccanismo alternativo/complementare per la comunicazione tra iframes
window.addEventListener('message', event => {
    // In produzione, verifica l'origine dell'evento per sicurezza: event.origin
    // console.log('Messaggio ricevuto dall\'iframe (treeview.js):', event.data); // Log rimosso
    // Puoi aggiungere qui la logica per gestire messaggi specifici dalla pagina principale
    // Esempio: se la pagina principale invia un messaggio per selezionare un percorso
    // if (event.data.type === 'select_path') {
    //     selectItem(event.data.payload.storageName, event.data.payload.path);
    // }
});
