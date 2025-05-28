// Si assume che window.config sia definito globalmente (es. da config.js)
// e che window.cacheService e window.websocket_service siano definiti prima che questo script venga eseguito
// o che la loro disponibilità sia verificata prima dell'uso.

// --------------- data_service.js ---------------
const data_service_module = (() => {
    const pendingRequests = new Map(); // requestKey -> Promise
    const MAX_BACKGROUND_PREFETCH_PAGES = 3; 
    const PREFETCH_DELAY_MS = 750;        
    const PREFETCH_PAGE_DELAY_MS = 300;   

    function getRequestKey(storageName, dirPath, page, itemsPerPage, options = {}) {
        return `${storageName}:${dirPath || '/'}:${page}:${itemsPerPage}:${options.onlyDirectories || false}`;
    }

    async function fetchDirectoryPageInternal(storageName, dirPath, page, itemsPerPage, options = {}) {
        const requestKey = getRequestKey(storageName, dirPath, page, itemsPerPage, options);

        const localCacheService = window.cacheService; // Accedi una volta per evitare accessi multipli a window
        if (!localCacheService) {
            console.error("DataService: cacheService non disponibile!");
            return Promise.reject({error_type: "service_unavailable", message: "Cache service not available."});
        }

        const cachedData = localCacheService.get(storageName, dirPath, page, itemsPerPage, options.onlyDirectories);
        if (cachedData) {
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                console.debug(`DataService: Cache hit for ${requestKey}`);
            }
            return Promise.resolve(cachedData); 
        }

        if (pendingRequests.has(requestKey)) {
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                console.debug(`DataService: Request pending for ${requestKey}, awaiting existing promise.`);
            }
            return pendingRequests.get(requestKey);
        }
        
        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
             console.debug(`DataService: Cache miss for ${requestKey}. Sending new request to backend.`);
        }

        const promise = new Promise((resolve, reject) => {
            const localWebsocketService = window.websocket_service; // Accedi una volta
            if (!localWebsocketService || typeof localWebsocketService.sendMessage !== 'function' || typeof localWebsocketService.registerCallbackForRequestID !== 'function') {
                const errorMsg = "DataService: websocket_service or its methods are not available.";
                console.error(errorMsg);
                reject({ error_type: "service_unavailable", message: errorMsg });
                return;
            }

            const payload = {
                storage_name: storageName,
                dir_path: dirPath,
                page: page,
                items_per_page: itemsPerPage,
                name_filter: options.nameFilter || "",
                timestamp_filter: options.timestampFilter || "",
                only_directories: options.onlyDirectories || false
            };
            
            const requestID = localWebsocketService.sendMessage({type: 'list_directory', payload: payload});

            if (!requestID) { 
                const errorMsg = "DataService: Failed to send message, sendMessage returned null (WebSocket not ready or other issue).";
                console.error(errorMsg);
                reject({error_type: "send_error", message: errorMsg}); 
                return;
            }
            
            localWebsocketService.registerCallbackForRequestID(requestID,
                (responsePayload) => { 
                    if (localCacheService) {
                        const effectiveOnlyDirs = responsePayload.only_directories !== undefined ? responsePayload.only_directories : (options.onlyDirectories || false);
                        localCacheService.store(responsePayload.storage_name || storageName, responsePayload.dir_path !== undefined ? responsePayload.dir_path : dirPath, {
                            ...responsePayload, 
                            only_directories: effectiveOnlyDirs 
                        });
                        
                        const newlyCachedData = localCacheService.get(
                            responsePayload.storage_name || storageName, 
                            responsePayload.dir_path !== undefined ? responsePayload.dir_path : dirPath, 
                            responsePayload.page, 
                            responsePayload.items_per_page, 
                            effectiveOnlyDirs
                        );
                        resolve(newlyCachedData || responsePayload); 
                    } else {
                        resolve(responsePayload); 
                    }
                },
                (errorPayload) => { 
                    if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelError)) {
                        console.error(`DataService: Error for requestKey ${requestKey} (ReqID: ${requestID}):`, errorPayload);
                    }
                    reject(errorPayload);
                }
            );
        });

        pendingRequests.set(requestKey, promise);

        try {
            const result = await promise;
            return result;
        } finally {
            pendingRequests.delete(requestKey);
        }
    }

    let visibilityChangeHandler = null; // Per tener traccia del listener

    function startBackgroundPrefetch(storageName, dirPath, initialResponsePayload, options = {}) {
        if (!initialResponsePayload || initialResponsePayload.totalItems <= 0 || initialResponsePayload.itemsPerPage <= 0) {
            return;
        }

        const itemsPerPage = initialResponsePayload.items_per_page;
        const totalPages = Math.ceil(initialResponsePayload.totalItems / itemsPerPage);
        let nextPageToFetch = initialResponsePayload.page + 1;
        let prefetchedCount = 0;
        const onlyDirectoriesForPrefetch = options.onlyDirectories || false;

        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
            console.info(`DataService: Starting background prefetch for ${storageName}:${dirPath || '/'}. Total pages: ${totalPages}. Current page: ${initialResponsePayload.page}. Prefetching up to ${MAX_BACKGROUND_PREFETCH_PAGES} more pages. (onlyDirs: ${onlyDirectoriesForPrefetch})`);
        }

        // Rimuovi il vecchio listener se esiste, prima di aggiungerne uno nuovo
        if (visibilityChangeHandler) {
            document.removeEventListener("visibilitychange", visibilityChangeHandler);
        }

        async function fetchNext() {
            if (nextPageToFetch > totalPages || prefetchedCount >= MAX_BACKGROUND_PREFETCH_PAGES) {
                if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                    console.info(`DataService: Background prefetch complete or limit reached for ${storageName}:${dirPath || '/'}. Fetched ${prefetchedCount} pages.`);
                }
                document.removeEventListener("visibilitychange", visibilityChangeHandler); 
                return;
            }
            
            const localCacheService = window.cacheService;
            if (!localCacheService) {
                 if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelWarning)) {
                    console.warn("DataService (prefetch): cacheService non disponibile. Interruzione prefetch.");
                }
                document.removeEventListener("visibilitychange", visibilityChangeHandler);
                return;
            }

            if (localCacheService.get(storageName, dirPath, nextPageToFetch, itemsPerPage, onlyDirectoriesForPrefetch)) {
                if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                     console.debug(`DataService: Page ${nextPageToFetch} for ${storageName}:${dirPath || '/'} (onlyDirs: ${onlyDirectoriesForPrefetch}) already in cache during prefetch. Skipping.`);
                }
                nextPageToFetch++;
                prefetchedCount++;
                setTimeout(fetchNext, PREFETCH_PAGE_DELAY_MS / 2); 
                return;
            }
            
            if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
                 console.debug(`DataService: Background prefetching page ${nextPageToFetch} for ${storageName}:${dirPath || '/'} (onlyDirs: ${onlyDirectoriesForPrefetch})`);
            }
            try {
                await fetchDirectoryPageInternal(storageName, dirPath, nextPageToFetch, itemsPerPage, options);
                prefetchedCount++;
            } catch (error) {
                if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelError)) {
                    console.error(`DataService: Error during background prefetch for ${storageName}:${dirPath || '/'}, page ${nextPageToFetch}:`, error);
                }
            } finally {
                nextPageToFetch++;
                if (document.hidden) { 
                     if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                        console.info(`DataService: Tab is hidden, pausing background prefetch for ${storageName}:${dirPath || '/'}.`);
                    }
                    return; // Pausa, il visibilityChangeHandler riprenderà
                }
                setTimeout(fetchNext, PREFETCH_PAGE_DELAY_MS);
            }
        }
        
        visibilityChangeHandler = () => { // Assegna alla variabile di scope superiore
            if (!document.hidden && nextPageToFetch <= totalPages && prefetchedCount < MAX_BACKGROUND_PREFETCH_PAGES) {
                if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
                    console.info(`DataService: Tab became visible, resuming background prefetch for ${storageName}:${dirPath || '/'}.`);
                }
                fetchNext(); 
            }
            // Non rimuovere il listener qui, potrebbe servire di nuovo se la tab viene nascosta e poi mostrata più volte
        };
        document.addEventListener("visibilitychange", visibilityChangeHandler);


        setTimeout(fetchNext, PREFETCH_DELAY_MS); 
    }

    async function fetchFileSystemsInternal() {
        if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelDebug)) {
            console.debug("DataService: Chiamata a fetchFileSystemsInternal.");
        }
        const requestKey = "get_filesystems_request"; 

        if (pendingRequests.has(requestKey)) {
            if (window.config && config.IsLogLevel(config.LogLevelDebug)) {
                console.debug("DataService: Richiesta get_filesystems già pendente.");
            }
            return pendingRequests.get(requestKey);
        }

        const promise = new Promise((resolve, reject) => {
            const localWebsocketService = window.websocket_service;
            if (!localWebsocketService || typeof localWebsocketService.sendMessage !== 'function') {
                const errorMsg = "DataService: websocket_service non disponibile per fetchFileSystems.";
                console.error(errorMsg);
                reject({ error_type: "service_unavailable", message: errorMsg });
                return;
            }
            const requestID = localWebsocketService.sendMessage({ type: 'get_filesystems', payload: null });
            if (!requestID) {
                const errorMsg = "DataService: Fallimento invio messaggio get_filesystems (sendMessage ha restituito null).";
                console.error(errorMsg);
                reject({ error_type: "send_error", message: errorMsg });
                return;
            }
            localWebsocketService.registerCallbackForRequestID(requestID, 
                (payload) => { 
                     if (Array.isArray(payload)) {
                        resolve(payload);
                    } else {
                        console.error("DataService: Payload per get_filesystems non è un array:", payload);
                        reject({error_type: "invalid_payload", message: "Formato dati storages non valido."});
                    }
                }, 
                (errorPayload) => { 
                    console.error("DataService: Errore ricevendo get_filesystems:", errorPayload);
                    reject(errorPayload);
                }
            );
        });
        
        pendingRequests.set(requestKey, promise);
        try {
            return await promise;
        } finally {
            pendingRequests.delete(requestKey);
        }
    }

    return {
        loadDirectory: async (storageName, dirPath, itemsPerPageForView, options = {}) => {
            if (window.showFilelistLoadingSpinner) window.showFilelistLoadingSpinner();
            
            try {
                const initialPayload = await fetchDirectoryPageInternal(storageName, dirPath, 1, itemsPerPageForView, {...options, isInitialLoad: true });
                if (initialPayload) {
                    // Avvia il pre-fetching solo se la richiesta iniziale era per "tutto" (non solo directory)
                    // e se ci sono più pagine.
                    if (initialPayload.totalItems > itemsPerPageForView && (options.onlyDirectories === false || options.onlyDirectories === undefined)) { 
                        startBackgroundPrefetch(storageName, dirPath, 
                            {...initialPayload, page: 1, items_per_page: itemsPerPageForView}, 
                            options // Passa le opzioni originali (incluso onlyDirectories)
                        );
                    }
                    return initialPayload; 
                }
                return null; 
            } catch (error) {
                if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelError)) {
                    console.error(`DataService: Failed to load initial directory ${storageName}:${dirPath || '/'}`, error);
                }
                throw error; 
            } finally {
                if (window.hideFilelistLoadingSpinner) window.hideFilelistLoadingSpinner();
            }
        },
        fetchPage: async (storageName, dirPath, page, itemsPerPage, options = {}) => {
             if (window.showFilelistLoadingSpinner) window.showFilelistLoadingSpinner();
             try {
                return await fetchDirectoryPageInternal(storageName, dirPath, page, itemsPerPage, options);
             } finally {
                if (window.hideFilelistLoadingSpinner) window.hideFilelistLoadingSpinner();
             }
        },
        fetchFileSystems: fetchFileSystemsInternal,
        invalidateCacheForPath: (sn, dp, ip, od) => { 
            const localCacheService = window.cacheService;
            if (localCacheService) localCacheService.invalidatePath(sn, dp, ip, od); 
            else if(window.config && config.IsLogLevel(config.IsLogLevelWarning)) console.warn("DataService: cacheService.invalidatePath chiamato ma cacheService non disponibile.");
        },
        invalidateAllCache: () => { 
            const localCacheService = window.cacheService;
            if (localCacheService) localCacheService.invalidateAll();
            else if(window.config && config.IsLogLevel(config.IsLogLevelWarning)) console.warn("DataService: cacheService.invalidateAll chiamato ma cacheService non disponibile.");
        }
    };
})();

window.dataService = data_service_module;

if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
    console.info("data_service.js caricato e window.dataService definito.");
}

window.data_service_ready_flag = true;
const dataServiceEventReady = new CustomEvent('dataServiceReady');
window.dispatchEvent(dataServiceEventReady);

if (window.config && config.IsLogLevel && config.IsLogLevel(config.LogLevelInfo)) {
    console.info("DataService: Flag 'data_service_ready_flag' impostato e evento 'dataServiceReady' emesso.");
}
