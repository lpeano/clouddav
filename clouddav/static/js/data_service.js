// Si assume che window.config sia definito globalmente.
// Questo script attenderà che window.cacheService e window.websocket_service siano pronti.

// --------------- data_service.js ---------------
const data_service_module = (() => {
    const pendingRequests = new Map(); 
    const MAX_BACKGROUND_PREFETCH_PAGES = 3; 
    const PREFETCH_DELAY_MS = 750;        
    const PREFETCH_PAGE_DELAY_MS = 300;   

    // Flag per tracciare la prontezza delle dipendenze
    let internalCacheServiceReady = window.cache_service_ready_flag || false;
    let internalWebsocketServiceReady = window.websocket_service_ready_flag || false;

    if (!internalCacheServiceReady) {
        window.addEventListener('cacheServiceReady', () => {
            if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info("DataService: Evento 'cacheServiceReady' ricevuto.");
            internalCacheServiceReady = true;
            checkAndSignalDataServiceReady();
        }, { once: true });
    }
    if (!internalWebsocketServiceReady) {
        window.addEventListener('websocketServiceReady', () => {
            if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info("DataService: Evento 'websocketServiceReady' ricevuto.");
            internalWebsocketServiceReady = true;
            checkAndSignalDataServiceReady();
        }, { once: true });
    }

    function checkAndSignalDataServiceReady() {
        if (internalCacheServiceReady && internalWebsocketServiceReady && !window.data_service_ready_flag) {
            window.data_service_ready_flag = true;
            const event = new CustomEvent('dataServiceReady');
            window.dispatchEvent(event);
            if (window.config && typeof config.IsLogLevel === 'function' && config.IsLogLevel(config.LogLevelInfo)) {
                console.info("DataService: Tutte le dipendenze pronte. Flag 'data_service_ready_flag' impostato e evento 'dataServiceReady' emesso.");
            }
        }
    }
    // Controlla subito se le dipendenze sono già pronte (es. se data_service.js è caricato dopo i suoi deps)
    // È importante chiamarlo dopo aver definito i listener per il caso in cui gli eventi siano già scattati.
    // Se questo script è caricato DOPO cache_service e websocket_service, i flag potrebbero essere già true.
    if (window.cache_service_ready_flag && window.websocket_service_ready_flag) {
        internalCacheServiceReady = true;
        internalWebsocketServiceReady = true;
        checkAndSignalDataServiceReady();
    }


    function getRequestKey(storageName, dirPath, page, itemsPerPage, options = {}) {
        return `${storageName}:${dirPath || '/'}:${page}:${itemsPerPage}:${options.onlyDirectories || false}`;
    }

    async function fetchDirectoryPageInternal(storageName, dirPath, page, itemsPerPage, options = {}) {
        // Questo controllo ora è più robusto perché data_service attende le sue dipendenze
        if (!internalCacheServiceReady || !internalWebsocketServiceReady) {
            const errorMsg = "DataService: Dipendenze (cacheService o websocket_service) non ancora pronte per fetchDirectoryPageInternal.";
            console.error(errorMsg);
            return Promise.reject({ error_type: "dependency_not_ready", message: errorMsg });
        }

        const requestKey = getRequestKey(storageName, dirPath, page, itemsPerPage, options);
        const localCacheService = window.cacheService; 

        if (!localCacheService || typeof localCacheService.get !== 'function') {
             console.error("DataService: cacheService o cacheService.get non disponibile in fetchDirectoryPageInternal.");
             return Promise.reject({ error_type: "service_unavailable", message: "CacheService.get non disponibile."});
        }

        const cachedData = localCacheService.get(storageName, dirPath, page, itemsPerPage, options.onlyDirectories);
        if (cachedData) {
            if (window.config && config.IsLogLevel(config.LogLevelDebug)) console.debug(`DataService: Cache hit for ${requestKey}`);
            return Promise.resolve(cachedData); 
        }

        if (pendingRequests.has(requestKey)) {
            if (window.config && config.IsLogLevel(config.LogLevelDebug)) console.debug(`DataService: Request pending for ${requestKey}, awaiting existing promise.`);
            return pendingRequests.get(requestKey);
        }
        
        if (window.config && config.IsLogLevel(config.LogLevelDebug)) console.debug(`DataService: Cache miss for ${requestKey}. Sending new request to backend.`);

        const promise = new Promise((resolve, reject) => {
            const localWebsocketService = window.websocket_service;
            if (!localWebsocketService || typeof localWebsocketService.sendMessage !== 'function') { 
                const errorMsg = "DataService: websocket_service.sendMessage non disponibile.";
                console.error(errorMsg);
                reject({ error_type: "service_unavailable", message: errorMsg });
                return;
            }

            const payload = { 
                storage_name: storageName, dir_path: dirPath, page: page, items_per_page: itemsPerPage,
                name_filter: options.nameFilter || "", timestamp_filter: options.timestampFilter || "",
                only_directories: options.onlyDirectories || false
            };
            const requestID = localWebsocketService.sendMessage({type: 'list_directory', payload: payload});

            if (!requestID) { 
                const errorMsg = "DataService: Failed to send message, sendMessage returned null.";
                console.error(errorMsg);
                reject({error_type: "send_error", message: errorMsg}); 
                return;
            }
            
            localWebsocketService.registerCallbackForRequestID(requestID,
                (responsePayload) => { 
                    if (localCacheService) {
                        const effectiveOnlyDirs = responsePayload.only_directories !== undefined ? responsePayload.only_directories : (options.onlyDirectories || false);
                        localCacheService.store(responsePayload.storage_name || storageName, responsePayload.dir_path !== undefined ? responsePayload.dir_path : dirPath, {
                            ...responsePayload, only_directories: effectiveOnlyDirs 
                        });
                        const newlyCachedData = localCacheService.get(responsePayload.storage_name || storageName, responsePayload.dir_path !== undefined ? responsePayload.dir_path : dirPath, responsePayload.page, responsePayload.items_per_page, effectiveOnlyDirs);
                        resolve(newlyCachedData || responsePayload); 
                    } else { resolve(responsePayload); }
                },
                (errorPayload) => { 
                    if (window.config && config.IsLogLevel(config.LogLevelError)) console.error(`DataService: Error for requestKey ${requestKey} (ReqID: ${requestID}):`, errorPayload);
                    reject(errorPayload);
                }
            );
        });

        pendingRequests.set(requestKey, promise);
        try { return await promise; } finally { pendingRequests.delete(requestKey); }
    }

    let visibilityChangeHandler = null; 
    function startBackgroundPrefetch(storageName, dirPath, initialResponsePayload, options = {}) {
        if (!internalCacheServiceReady) { 
            if (window.config && config.IsLogLevel(config.LogLevelWarning)) console.warn("DataService (prefetch): CacheService non pronto, prefetch annullato.");
            return;
        }
        if (!initialResponsePayload || initialResponsePayload.totalItems <= 0 || initialResponsePayload.itemsPerPage <= 0) return;
        const itemsPerPage = initialResponsePayload.items_per_page;
        const totalPages = Math.ceil(initialResponsePayload.totalItems / itemsPerPage);
        let nextPageToFetch = initialResponsePayload.page + 1;
        let prefetchedCount = 0;
        const onlyDirectoriesForPrefetch = options.onlyDirectories || false;

        if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info(`DataService: Starting background prefetch for ${storageName}:${dirPath || '/'}. Total pages: ${totalPages}. From page: ${nextPageToFetch}.`);
        
        if (visibilityChangeHandler) document.removeEventListener("visibilitychange", visibilityChangeHandler);

        async function fetchNext() {
            if (nextPageToFetch > totalPages || prefetchedCount >= MAX_BACKGROUND_PREFETCH_PAGES) {
                if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info(`DataService: Prefetch complete/limit for ${storageName}:${dirPath || '/'}.`);
                document.removeEventListener("visibilitychange", visibilityChangeHandler);
                return;
            }
            const localCacheService = window.cacheService;
            if (!localCacheService) {
                if (window.config && config.IsLogLevel(config.LogLevelWarning)) console.warn("DataService (prefetch): cacheService non disponibile. Interruzione.");
                document.removeEventListener("visibilitychange", visibilityChangeHandler);
                return;
            }
            if (localCacheService.get(storageName, dirPath, nextPageToFetch, itemsPerPage, onlyDirectoriesForPrefetch)) {
                if (window.config && config.IsLogLevel(config.LogLevelDebug)) console.debug(`DataService: Page ${nextPageToFetch} (prefetch) for ${storageName}:${dirPath || '/'} in cache. Skipping.`);
                nextPageToFetch++; prefetchedCount++;
                setTimeout(fetchNext, PREFETCH_PAGE_DELAY_MS / 2);
                return;
            }
            if (window.config && config.IsLogLevel(config.LogLevelDebug)) console.debug(`DataService: Prefetching page ${nextPageToFetch} for ${storageName}:${dirPath || '/'}`);
            try {
                await fetchDirectoryPageInternal(storageName, dirPath, nextPageToFetch, itemsPerPage, options);
                prefetchedCount++;
            } catch (error) {
                if (window.config && config.IsLogLevel(config.LogLevelError)) console.error(`DataService: Error prefetching page ${nextPageToFetch} for ${storageName}:${dirPath || '/'}:`, error);
            } finally {
                nextPageToFetch++;
                if (document.hidden) {
                    if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info(`DataService: Tab hidden, pausing prefetch for ${storageName}:${dirPath || '/'}.`);
                    return;
                }
                setTimeout(fetchNext, PREFETCH_PAGE_DELAY_MS);
            }
        }
        visibilityChangeHandler = () => {
            if (!document.hidden && nextPageToFetch <= totalPages && prefetchedCount < MAX_BACKGROUND_PREFETCH_PAGES) {
                if (window.config && config.IsLogLevel(config.LogLevelInfo)) console.info(`DataService: Tab visible, resuming prefetch for ${storageName}:${dirPath || '/'}.`);
                fetchNext();
            }
        };
        document.addEventListener("visibilitychange", visibilityChangeHandler);
        setTimeout(fetchNext, PREFETCH_DELAY_MS);
    }

    async function fetchFileSystemsInternal() {
        if (!internalWebsocketServiceReady) {
            const errorMsg = "DataService: websocket_service non pronto per fetchFileSystems.";
            console.error(errorMsg);
            return Promise.reject({ error_type: "dependency_not_ready", message: errorMsg });
        }
        if (window.config && config.IsLogLevel(config.LogLevelDebug)) console.debug("DataService: Chiamata a fetchFileSystemsInternal.");
        const requestKey = "get_filesystems_request";
        if (pendingRequests.has(requestKey)) {
             if (window.config && config.IsLogLevel(config.LogLevelDebug)) console.debug("DataService: Richiesta get_filesystems già pendente, restituisco promise esistente.");
            return pendingRequests.get(requestKey);
        }

        const promise = new Promise((resolve, reject) => {
            const localWebsocketService = window.websocket_service;
            if (!localWebsocketService || typeof localWebsocketService.sendMessage !== 'function') { 
                const errorMsg = "DataService: WS service o sendMessage mancante per get_filesystems";
                console.error(errorMsg);
                reject({error_type:"service_unavailable", message: errorMsg}); return; 
            }
            const requestID = localWebsocketService.sendMessage({ type: 'get_filesystems', payload: null });
            if (!requestID) { 
                const errorMsg = "DataService: WS send failed for get_filesystems, sendMessage ha restituito null.";
                console.error(errorMsg);
                reject({error_type:"send_error", message: errorMsg}); return; 
            }
            localWebsocketService.registerCallbackForRequestID(requestID, 
                (payload) => { 
                     if (Array.isArray(payload)) resolve(payload);
                     else { console.error("DataService: Payload get_filesystems non array:", payload); reject({error_type: "invalid_payload", message: "Formato dati storages non valido."});}
                }, 
                (errorPayload) => { console.error("DataService: Errore get_filesystems:", errorPayload); reject(errorPayload); }
            );
        });
        pendingRequests.set(requestKey, promise);
        try { return await promise; } finally { pendingRequests.delete(requestKey); }
    }

    return {
        loadDirectory: async (storageName, dirPath, itemsPerPageForView, options = {}) => {
            if (!internalCacheServiceReady || !internalWebsocketServiceReady) {
                 console.error("DataService.loadDirectory: Dipendenze non pronte.");
                 return Promise.reject({error_type: "dependency_not_ready", message:"DataService non pronto (dipendenze mancanti)."});
            }
            if (window.showFilelistLoadingSpinner) window.showFilelistLoadingSpinner();
            try {
                const initialPayload = await fetchDirectoryPageInternal(storageName, dirPath, 1, itemsPerPageForView, {...options, isInitialLoad: true });
                if (initialPayload) {
                    if (initialPayload.totalItems > itemsPerPageForView && (options.onlyDirectories === false || options.onlyDirectories === undefined)) { 
                        startBackgroundPrefetch(storageName, dirPath, {...initialPayload, page: 1, items_per_page: itemsPerPageForView}, options);
                    }
                    return initialPayload; 
                }
                return null; 
            } catch (error) {
                if (window.config && config.IsLogLevel(config.LogLevelError)) console.error(`DataService: Failed to load initial directory ${storageName}:${dirPath || '/'}`, error);
                throw error; 
            } finally {
                if (window.hideFilelistLoadingSpinner) window.hideFilelistLoadingSpinner();
            }
        },
        fetchPage: async (storageName, dirPath, page, itemsPerPage, options = {}) => {
            if (!internalCacheServiceReady || !internalWebsocketServiceReady) {
                console.error(`DataService.fetchPage: Dipendenze non pronte (cache: ${internalCacheServiceReady}, ws: ${internalWebsocketServiceReady}).`);
                return Promise.reject({error_type: "dependency_not_ready", message:"DataService non pronto (dipendenze mancanti)."});
            }
            if (window.showFilelistLoadingSpinner) window.showFilelistLoadingSpinner();
            try { return await fetchDirectoryPageInternal(storageName, dirPath, page, itemsPerPage, options); }
            finally { if (window.hideFilelistLoadingSpinner) window.hideFilelistLoadingSpinner(); }
        },
        fetchFileSystems: fetchFileSystemsInternal,
        invalidateCacheForPath: (sn, dp, ip, od) => { 
            const localCacheService = window.cacheService;
            if (localCacheService) localCacheService.invalidatePath(sn, dp, ip, od); 
            else if(window.config && config.IsLogLevel(config.LogLevelWarning)) console.warn("DataService: cacheService.invalidatePath chiamato ma cacheService non disponibile.");
        },
        invalidateAllCache: () => { 
            const localCacheService = window.cacheService;
            if (localCacheService) localCacheService.invalidateAll();
            else if(window.config && config.IsLogLevel(config.LogLevelWarning)) console.warn("DataService: cacheService.invalidateAll chiamato ma cacheService non disponibile.");
        }
    };
})();

window.dataService = data_service_module; 

if (window.config && typeof config.IsLogLevel === 'function' && config.IsLogLevel(config.LogLevelInfo)) { 
    console.info("data_service.js caricato. In attesa di cacheService e websocketService per emettere dataServiceReady.");
}
// L'evento 'dataServiceReady' viene emesso da checkAndSignalDataServiceReady quando le dipendenze sono soddisfatte.
// La chiamata iniziale a checkAndSignalDataServiceReady è già stata fatta sopra.
