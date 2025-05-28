// Simula un oggetto di configurazione per i log, simile a quello Go
// In un'applicazione reale, questo verrebbe dalla configurazione caricata.
const config = {
    LogLevelDebug: 'DEBUG',
    LogLevelInfo: 'INFO',
    currentLogLevel: 'DEBUG', // Imposta a 'INFO' o 'DEBUG' per vedere i log

    IsLogLevel: function(levelToCheck) {
        if (this.currentLogLevel === this.LogLevelDebug) {
            return true;
        }
        if (this.currentLogLevel === this.LogLevelInfo) {
            return levelToCheck === this.LogLevelInfo;
        }
        return false;
    }
};

// --------------- cache_service.js ---------------
const cacheService = (() => {
    const cache = {}; // Struttura: { "storageName:dirPath:itemsPerPage:onlyDirs": { items: [], totalItems: 0, itemsPerPage: 0, pagesLoaded: new Set(), lastRefreshed: null, onlyDirectories: false } }
    const DEFAULT_ITEMS_PER_PAGE_FOR_CACHE = 50;

    function getCacheKey(storageName, dirPath, itemsPerPage, onlyDirectories) {
        return `${storageName}:${dirPath || '/'}:${itemsPerPage}:${onlyDirectories || false}`;
    }

    return {
        get: (storageName, dirPath, page, itemsPerPage, onlyDirectories = false) => {
            const cacheKey = getCacheKey(storageName, dirPath, itemsPerPage, onlyDirectories);
            const entry = cache[cacheKey];

            if (!entry || !entry.pagesLoaded.has(page)) {
                return null;
            }

            const startIndex = (page - 1) * itemsPerPage;
            const endIndex = Math.min(startIndex + itemsPerPage, entry.totalItems);
            
            let pageItems = [];
            for (let i = startIndex; i < endIndex; i++) {
                if (entry.items[i] !== null && entry.items[i] !== undefined) {
                    pageItems.push(entry.items[i]);
                } else {
                    if (config.IsLogLevel(config.LogLevelDebug)) {
                        console.warn(`CacheService: Cache HIT for ${cacheKey}, page ${page}, ma item ${i} è null/undefined. Potrebbe essere incompleta.`);
                    }
                }
            }
            
            if (pageItems.length < itemsPerPage && (startIndex + pageItems.length) < entry.totalItems && entry.pagesLoaded.has(page)) {
                 // Questo log potrebbe essere troppo verboso se la pagina è l'ultima e parziale.
                 // Si verifica se la pagina è contrassegnata come caricata ma non abbiamo tutti gli item attesi E non siamo alla fine.
                if (config.IsLogLevel(config.LogLevelDebug)) {
                     // console.warn(`CacheService: Cache HIT for ${cacheKey}, page ${page}, ma recuperati ${pageItems.length} items invece di ${itemsPerPage}. TotalItems: ${entry.totalItems}`);
                }
            }

            if (config.IsLogLevel(config.LogLevelDebug)) {
                console.debug(`CacheService: Cache HIT for ${cacheKey}, page ${page}. Returning ${pageItems.length} items.`);
            }
            return {
                items: pageItems,
                totalItems: entry.totalItems,
                page: page,
                itemsPerPage: entry.itemsPerPage,
                onlyDirectories: entry.onlyDirectories
            };
        },

        store: (storageName, dirPath, responsePayload) => {
            const itemsPerPage = responsePayload.items_per_page || DEFAULT_ITEMS_PER_PAGE_FOR_CACHE;
            const onlyDirectories = responsePayload.only_directories || false;
            const cacheKey = getCacheKey(storageName, dirPath, itemsPerPage, onlyDirectories);
            
            let entry = cache[cacheKey];

            if (!entry) {
                entry = {
                    items: new Array(responsePayload.total_items || 0).fill(null),
                    totalItems: responsePayload.total_items || 0,
                    itemsPerPage: itemsPerPage,
                    pagesLoaded: new Set(),
                    lastRefreshed: Date.now(),
                    onlyDirectories: onlyDirectories
                };
                cache[cacheKey] = entry;
            } else {
                if (entry.totalItems !== responsePayload.total_items) {
                    if (config.IsLogLevel(config.LogLevelInfo)) {
                        console.info(`CacheService: totalItems cambiato per ${cacheKey} da ${entry.totalItems} a ${responsePayload.total_items}. Resetto array items e pagine caricate.`);
                    }
                    entry.items = new Array(responsePayload.total_items || 0).fill(null);
                    entry.pagesLoaded.clear(); 
                }
                entry.totalItems = responsePayload.total_items || 0;
                entry.itemsPerPage = itemsPerPage; 
                entry.onlyDirectories = onlyDirectories;
            }
            
            const startIndex = (responsePayload.page - 1) * itemsPerPage;
            for (let i = 0; i < responsePayload.items.length; i++) {
                if (startIndex + i < entry.items.length) {
                    entry.items[startIndex + i] = responsePayload.items[i];
                } else {
                     if (config.IsLogLevel(config.LogLevelDebug)) {
                        console.warn(`CacheService: Tentativo di scrivere item ${startIndex + i} per ${cacheKey} oltre la dimensione preallocata ${entry.items.length}. Item:`, responsePayload.items[i]);
                     }
                }
            }
            entry.pagesLoaded.add(responsePayload.page);
            entry.lastRefreshed = Date.now();

            if (config.IsLogLevel(config.LogLevelDebug)) {
                const loadedCount = entry.items.filter(i => i !== null).length;
                console.debug(`CacheService: Stored page ${responsePayload.page} for ${cacheKey} (itemsPerPage: ${itemsPerPage}, onlyDirs: ${onlyDirectories}). Total cached items: ${loadedCount}/${entry.totalItems}. Pages loaded:`, Array.from(entry.pagesLoaded));
            }
        },

        invalidatePath: (storageName, dirPath, itemsPerPage = null, onlyDirectories = null) => {
            if (itemsPerPage !== null && onlyDirectories !== null) {
                const cacheKey = getCacheKey(storageName, dirPath, itemsPerPage, onlyDirectories);
                if (config.IsLogLevel(config.LogLevelInfo)) {
                    console.info(`CacheService: Invalidating specific cache view for ${cacheKey}`);
                }
                delete cache[cacheKey];
            } else {
                if (config.IsLogLevel(config.LogLevelInfo)) {
                    console.info(`CacheService: Invalidating ALL cache views for path ${storageName}:${dirPath || '/'}`);
                }
                for (const key in cache) {
                    if (key.startsWith(`${storageName}:${dirPath || '/'}:`)) {
                        delete cache[key];
                    }
                }
            }
            // window.dispatchEvent(new CustomEvent('cacheInvalidated', { detail: { storageName, dirPath, itemsPerPage, onlyDirectories } }));
        },

        invalidateAll: () => {
            if (config.IsLogLevel(config.LogLevelInfo)) {
                console.info("CacheService: Invalidating all cache.");
            }
            for (const key in cache) {
                delete cache[key];
            }
            // window.dispatchEvent(new CustomEvent('cacheInvalidatedAll'));
        }
    };
})();
