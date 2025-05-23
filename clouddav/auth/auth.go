package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"clouddav/config"

	"github.com/coreos/go-oidc/v3/oidc"
)

// ClaimsKey is the key to store user claims in the request context.
type ClaimsKey struct{}

// Provider OIDC and OAuth2 configuration
var (
	provider     *oidc.Provider
	oauth2Config oauth2.Config
	stateStore   = make(map[string]time.Time)
	stateStoreMutex sync.Mutex
)

const stateExpiry = 5 * time.Minute

// InitAzureAD initializes the OIDC provider and OAuth2 configuration.
// This is for USER authentication via Microsoft Entra ID.
func InitAzureAD(cfg *config.Config) error {
	if !cfg.EnableAuth {
		log.Println("User authentication disabled in configuration.")
		return nil
	}

	if cfg.AzureAD.TenantID == "" || cfg.AzureAD.ClientID == "" || cfg.AzureAD.RedirectURL == "" {
		return errors.New("incomplete Azure AD configuration for user authentication")
	}

	ctx := context.Background()
	issuerURL := fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", cfg.AzureAD.TenantID)
	log.Printf("[DEBUG] InitAzureAD: Issuer URL: %s", issuerURL)

	var err error
	provider, err = oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		log.Printf("Error creating OIDC provider for user authentication: %v", err)
		return fmt.Errorf("error creating OIDC provider for user authentication: %w", err)
	}

	log.Printf("[DEBUG] InitAzureAD: OIDC Provider Endpoint AuthURL: '%s'", provider.Endpoint().AuthURL)

	oauth2Config = oauth2.Config{
		ClientID:     cfg.AzureAD.ClientID,
		ClientSecret: cfg.AzureAD.ClientSecret,
		RedirectURL:  cfg.AzureAD.RedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes: []string{oidc.ScopeOpenID, "profile", "email", "offline_access", "https://graph.microsoft.com/.default"},
	}
	log.Printf("[DEBUG] InitAzureAD: oauth2Config initialized with ClientID: '%s', RedirectURL: '%s', Auth Endpoint URL: '%s'", oauth2Config.ClientID, oauth2Config.RedirectURL, oauth2Config.Endpoint.AuthURL)

	go cleanupExpiredStates()

	if config.IsLogLevel(config.LogLevelInfo) {
		log.Println("Azure AD user authentication initialization completed.")
	}
	return nil
}

// cleanupExpiredStates periodically removes expired states from the store.
func cleanupExpiredStates() {
	ticker := time.NewTicker(stateExpiry)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			stateStoreMutex.Lock()
			now := time.Now()
			for state, timestamp := range stateStore {
				if now.Sub(timestamp) > stateExpiry {
					delete(stateStore, state)
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Expired state removed: %s", state)
					}
				}
			}
			stateStoreMutex.Unlock()
		case <-context.Background().Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("State cleanup goroutine stopping.")
			}
			return
		}
	}
}

// GenerateState creates and stores a random state to prevent CSRF attacks.
func GenerateState() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("failed to generate random state: %w", err)
	}
	state := base64.URLEncoding.EncodeToString(b)

	stateStoreMutex.Lock()
	stateStore[state] = time.Now()
	stateStoreMutex.Unlock()

	log.Printf("[DEBUG] GenerateState: Generated state: %s", state)

	return state, nil
}

// VerifyState verifies that the received state is valid and not expired.
func VerifyState(state string) bool {
	stateStoreMutex.Lock()
	defer stateStoreMutex.Unlock()

	timestamp, ok := stateStore[state]
	if !ok {
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] VerifyState: State not found: %s", state)
		}
		return false // State not found
	}

	if time.Since(timestamp) > stateExpiry {
		delete(stateStore, state) // Remove expired state
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] VerifyState: State expired: %s", state)
		}
		return false // State expired
	}

	delete(stateStore, state) // Consume the state after successful verification
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] VerifyState: State verified and consumed: %s", state)
	}
	return true
}

// GetLoginURL generates the URL to start the authentication flow.
func GetLoginURL() (string, error) {
	state, err := GenerateState()
	if err != nil {
		return "", fmt.Errorf("unable to generate state: %w", err)
	}
	loginURL := oauth2Config.AuthCodeURL(state)
	log.Printf("[DEBUG] GetLoginURL: Login URL: %s", loginURL)
	return loginURL, nil
}

// HandleCallback handles the callback after authentication with Microsoft Entra ID.
// Restituisce l'ID Token, l'Access Token e un errore.
func HandleCallback(ctx context.Context, r *http.Request) (*oidc.IDToken, string, error) {
	state := r.URL.Query().Get("state")
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] HandleCallback: Received state from callback: %s", state)
	}
	if !VerifyState(state) {
		log.Println("[ERROR] HandleCallback: State verification failed")
		return nil, "", errors.New("invalid or missing OIDC state")
	}

	oauth2Token, err := oauth2Config.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		log.Printf("Error exchanging OAuth2 code: %v", err)
		return nil, "", fmt.Errorf("unable to exchange OAuth2 code: %w", err)
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] HandleCallback: OAuth2 Token received. Expires in: %s", oauth2Token.Expiry.Sub(time.Now()))
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		return nil, "", errors.New("no id_token present in OAuth2 response")
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] HandleCallback: Raw ID Token received (length: %d)", len(rawIDToken))
	}

	accessToken, ok := oauth2Token.Extra("access_token").(string)
	if !ok {
		// L'Access Token è necessario per chiamare Graph, quindi è un errore critico.
		return nil, "", errors.New("no access_token present in OAuth2 response")
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] HandleCallback: Access Token received (length: %d)", len(accessToken))
	}

	oidcConfig := &oidc.Config{
		ClientID: oauth2Config.ClientID,
	}

	idToken, err := provider.Verifier(oidcConfig).Verify(ctx, rawIDToken)
	if err != nil {
		log.Printf("Error verifying id_token: %v", err)
		return nil, "", fmt.Errorf("unable to verify id_token: %w", err)
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] HandleCallback: ID Token verified successfully. Subject: %s, Issuer: %s", idToken.Subject, idToken.Issuer)
	}

	return idToken, accessToken, nil
}

// UserClaims represents the user information extracted from the ID Token.
type UserClaims struct {
	Subject string   `json:"sub"`
	Name    string   `json:"name"`
	Email   string   `json:"email"`
	Groups  []string `json:"groups"`      // IDs of the groups the user is a member of (per autorizzazione, se si usa ID)
	GroupNames []string `json:"group_names,omitempty"` // Nomi dei gruppi (per logging/UI e match per nome)
}

// GetUserClaims extracts user information from the ID Token.
func GetUserClaims(idToken *oidc.IDToken) (*UserClaims, error) {
	claims := &UserClaims{}
	if err := idToken.Claims(claims); err != nil {
		log.Printf("Error extracting claims from id_token: %v", err)
		return nil, fmt.Errorf("unable to extract claims from id_token: %w", err)
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		claimsJSON, _ := json.MarshalIndent(claims, "", "  ")
		log.Printf("[DEBUG] GetUserClaims: Extracted claims from ID Token:\n%s", string(claimsJSON))
	}
	return claims, nil
}

// GraphGroupsResponse è la struttura per deserializzare la risposta JSON di Microsoft Graph.
type GraphGroupsResponse struct {
	Value []struct {
		DisplayName string `json:"displayName"` // Nome del gruppo (opzionale, utile per debug)
		ID          string `json:"id"`          // ID del gruppo
	} `json:"value"`
}

// GetUserGroupsFromGraph effettua una chiamata a Microsoft Graph per recuperare i gruppi dell'utente.
// Restituisce una slice di ID dei gruppi, una slice di nomi dei gruppi e un errore.
func GetUserGroupsFromGraph(ctx context.Context, accessToken string) ([]string, []string, error) {
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Println("[DEBUG] GetUserGroupsFromGraph: Starting Graph API call to retrieve user groups.")
	}
	graphEndpoint := "https://graph.microsoft.com/v1.0/me/transitiveMemberOf"

	req, err := http.NewRequestWithContext(ctx, "GET", graphEndpoint, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Graph request: %w", err)
	}
	req.Header.Add("Authorization", "Bearer "+accessToken)
	req.Header.Add("ConsistencyLevel", "eventual")

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to make Graph request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] GetUserGroupsFromGraph: Graph API response error body: %s", string(bodyBytes))
		}
		return nil, nil, fmt.Errorf("Graph API request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var graphResponse GraphGroupsResponse
	if err := json.NewDecoder(resp.Body).Decode(&graphResponse); err != nil {
		return nil, nil, fmt.Errorf("failed to decode Graph response: %w", err)
	}

	var groupIDs []string
	var groupNames []string
	for _, group := range graphResponse.Value {
		groupIDs = append(groupIDs, group.ID)
		groupNames = append(groupNames, group.DisplayName) // Estrai anche il nome del gruppo
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] GetUserGroupsFromGraph: Successfully retrieved %d group IDs: %v", len(groupIDs), groupIDs)
		log.Printf("[DEBUG] GetUserGroupsFromGraph: Successfully retrieved %d group names: %v", len(groupNames), groupNames)
	}

	return groupIDs, groupNames, nil
}

// IsUserAuthorized checks if the user has application-level access based on configured allowed groups.
// This check is now performed by matching against group names.
// This check is only performed if enable_auth is true.
func IsUserAuthorized(claims *UserClaims, cfg *config.Config) bool {
	if !cfg.EnableAuth {
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] IsUserAuthorized: Authentication disabled, user is authorized.")
		}
		return true // User authentication is disabled, application access is implicitly granted
	}
	if claims == nil {
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Println("IsUserAuthorized called with nil claims when enable_auth is true.")
		}
		return false // No claims means no authenticated user
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] IsUserAuthorized: Checking authorization for user '%s' (Email: %s).", claims.Subject, claims.Email)
		log.Printf("[DEBUG] IsUserAuthorized: User's groups (IDs): %v", claims.Groups)
		log.Printf("[DEBUG] IsUserAuthorized: User's groups (Names): %v", claims.GroupNames) // Logga anche i nomi
		log.Printf("[DEBUG] IsUserAuthorized: Configured allowed groups (Names expected): %v", cfg.AzureAD.AllowedGroups) // Nota per i nomi
	}

	if len(cfg.AzureAD.AllowedGroups) == 0 {
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] IsUserAuthorized: No allowed groups configured, user is authorized.")
		}
		return true // No global groups required, any authenticated user is authorized
	}

	// Crea una mappa per una ricerca efficiente dei nomi dei gruppi dell'utente
	userGroupNamesMap := make(map[string]bool)
	for _, groupName := range claims.GroupNames {
		userGroupNamesMap[groupName] = true
	}

	// Controlla se l'utente appartiene a uno dei gruppi consentiti (per nome)
	for _, allowedGroupName := range cfg.AzureAD.AllowedGroups {
		if userGroupNamesMap[allowedGroupName] {
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("[DEBUG] IsUserAuthorized: User '%s' is a member of allowed group name '%s'. Authorization granted.", claims.Email, allowedGroupName)
			}
			return true // L'utente appartiene ad almeno un gruppo consentito
		}
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("[DEBUG] IsUserAuthorized: User '%s' is not a member of any configured allowed group (by Name). Authorization denied.", claims.Email)
	}
	return false // L'utente non appartiene a nessun gruppo consentito
}
