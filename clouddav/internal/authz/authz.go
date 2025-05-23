package authz

import (
	"context"
	"errors"
	"log"

	// Import strings for ToLower or other string operations if needed
	"clouddav/auth"    // Importa il package auth per UserClaims
	"clouddav/config"  // Importa il package config per Config e StorageConfig
	"clouddav/storage" // Importa il package storage per StorageProvider e errori comuni
)

// CheckStorageAccess verifies if the user has the required permissions on a specific storage and path.
// This check is now performed by matching against group names.
// This check is only performed if enable_auth is true.
func CheckStorageAccess(ctx context.Context, claims *auth.UserClaims, storageName string, itemPath string, requiredAccess string, cfg *config.Config) error {
	if !cfg.EnableAuth {
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] authz.CheckStorageAccess: Authentication disabled, access implicitly granted.")
		}
		return nil // User authentication is disabled, access to storage is implicitly granted
	}
	if claims == nil {
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Println("authz.CheckStorageAccess called with nil claims when enable_auth is true.")
		}
		return storage.ErrPermissionDenied // No claims means no authenticated user, deny access
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] authz.CheckStorageAccess: Checking storage access for user '%s' (Email: %s) on storage '%s', path '%s' for '%s' access.", claims.Subject, claims.Email, storageName, itemPath, requiredAccess)
		log.Printf("[DEBUG] authz.CheckStorageAccess: User's groups (Names): %v", claims.GroupNames)
		log.Printf("[DEBUG] authz.CheckStorageAccess: Configured global admin groups: %v", cfg.GlobalAdminGroups)
	}

	// Step 1: Check if the user is a global administrator
	// Crea una mappa per una ricerca efficiente dei nomi dei gruppi dell'utente
	userGroupNamesMap := make(map[string]bool)
	for _, groupName := range claims.GroupNames {
		userGroupNamesMap[groupName] = true
	}

	for _, adminGroup := range cfg.GlobalAdminGroups {
		if userGroupNamesMap[adminGroup] {
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("[DEBUG] authz.CheckStorageAccess: User '%s' is a member of global admin group '%s'. Granting full access.", claims.Email, adminGroup)
			}
			return nil // Global admin has full access
		}
	}

	// Step 2: If not a global admin, proceed with granular storage permissions
	// Se non è un amministratore globale, procedi con i permessi granulari dello storage
	var storageCfg *config.StorageConfig
	for i := range cfg.Storages {
		if cfg.Storages[i].Name == storageName {
			storageCfg = &cfg.Storages[i]
			break
		}
	}

	if storageCfg == nil {
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("authz.CheckStorageAccess called for non-existent storage '%s'", storageName)
		}
		return errors.New("storage not found")
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] authz.CheckStorageAccess: Found storage config for '%s'. Configured permissions: %v", storageName, storageCfg.Permissions)
	}


	hasRead := false
	hasWrite := false

	// Check permissions defined in the storage configuration by matching group names
	for _, perm := range storageCfg.Permissions {
		if userGroupNamesMap[perm.GroupID] { // Confronta con il nome del gruppo
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("[DEBUG] authz.CheckStorageAccess: User '%s' is a member of configured group '%s' with access '%s' for storage '%s'.", claims.Email, perm.GroupID, perm.Access, storageName)
			}
			if perm.Access == "read" {
				hasRead = true
			} else if perm.Access == "write" {
				hasRead = true // Write implies read
				hasWrite = true
			}
		}
	}

	if requiredAccess == "read" && !hasRead {
		log.Printf("Access denied for user '%s': Read permission required for storage '%s', path '%s'. User does not have read access via configured groups.", claims.Email, storageName, itemPath)
		return storage.ErrPermissionDenied
	} else if requiredAccess == "write" && !hasWrite {
		log.Printf("Access denied for user '%s': Write permission required for storage '%s', path '%s'. User does not have write access via configured groups.", claims.Email, storageName, itemPath)
		return storage.ErrPermissionDenied
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] authz.CheckStorageAccess: Access granted for user '%s' on storage '%s' for '%s' operation based on granular permissions.", claims.Email, storageName, requiredAccess)
	}
	return nil // Access granted
}


// GetAccessibleStorages returns the list of storage configurations the user has at least read access to.
// This is used by the frontend to build the initial treeview.
// If enable_auth is false, all configured storages are returned.
// If enable_auth is true, all configured storages are returned if the user is a global admin.
// Otherwise, only storages where the user has read access (on the root path "") are returned.
func GetAccessibleStorages(ctx context.Context, claims *auth.UserClaims, cfg *config.Config) []config.StorageConfig {
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Println("authz.GetAccessibleStorages chiamato.")
	}

	accessible := []config.StorageConfig{}
	allStorages := cfg.Storages

	if allStorages == nil {
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Println("Configured storages list is nil, returning empty slice.")
		}
		return []config.StorageConfig{}
	}

	if !cfg.EnableAuth {
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Println("User authentication disabled, returning all configured storages.")
		}
		accessible = make([]config.StorageConfig, len(cfg.Storages))
		copy(accessible, cfg.Storages)
		return accessible
	}

	if config.IsLogLevel(config.LogLevelInfo) {
		log.Println("User authentication enabled, filtering accessible storages based on permissions.")
	}
	if claims == nil {
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Println("authz.GetAccessibleStorages called with nil claims when enable_auth is true. Returning empty slice.")
		}
		return []config.StorageConfig{}
	}

	// Crea una mappa per una ricerca efficiente dei nomi dei gruppi dell'utente
	userGroupNamesMap := make(map[string]bool)
	for _, groupName := range claims.GroupNames {
		userGroupNamesMap[groupName] = true
	}

	// Check if the user is a global administrator
	for _, adminGroup := range cfg.GlobalAdminGroups {
		if userGroupNamesMap[adminGroup] {
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("[DEBUG] authz.GetAccessibleStorages: User '%s' is a global admin. Returning all configured storages.", claims.Email)
			}
			// Se l'utente è un amministratore globale, restituisci tutti gli storage
			accessible = make([]config.StorageConfig, len(cfg.Storages))
			copy(accessible, cfg.Storages)
			return accessible
		}
	}

	// If not a global admin, filter based on granular storage permissions
	for _, storageCfg := range allStorages {
		// Per la lista degli storage accessibili, controlliamo se l'utente ha almeno un permesso di "read"
		// per la root dello storage (path "").
		hasReadAccessToStorage := false
		for _, perm := range storageCfg.Permissions {
			if userGroupNamesMap[perm.GroupID] && (perm.Access == "read" || perm.Access == "write") {
				hasReadAccessToStorage = true
				break
			}
		}

		if hasReadAccessToStorage {
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Storage '%s' is accessible to user '%s', adding to list.", storageCfg.Name, claims.Email)
			}
			accessible = append(accessible, storageCfg)
		} else {
             log.Printf("Storage '%s' is not accessible to user '%s': No read permission via configured groups.", storageCfg.Name, claims.Email)
        }

		select {
		case <-ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Context cancelled during authz.GetAccessibleStorages: %v", ctx.Err())
			}
			return []config.StorageConfig{}
		default:
		}
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("authz.GetAccessibleStorages terminato. Restituiti %d storage accessibili.", len(accessible))
	}
	return accessible
}
