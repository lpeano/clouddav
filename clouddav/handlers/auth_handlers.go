package handlers

import (
	"log"
	"net/http"
	"net/url"
	"time"

	"clouddav/auth" // Assicurati che questo import sia corretto
	"clouddav/config"
	"encoding/json"
	"fmt"
)

// HandleLogin gestisce la richiesta di login, reindirizzando l'utente alla pagina di login di Azure AD.
func HandleLogin(appConfig *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !appConfig.EnableAuth {
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("[DEBUG] HandleLogin: Authentication disabled, redirecting to home.")
			}
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] HandleLogin: Initiating Azure AD login flow.")
		}

		loginURL, err := auth.GetLoginURL() // auth package gestisce la logica OIDC
		if err != nil {
			log.Printf("Error retrieving login URL: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, loginURL, http.StatusFound)
	}
}

// HandleCallback gestisce il callback da Azure AD dopo l'autenticazione.
func HandleCallback(appConfig *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !appConfig.EnableAuth {
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("[DEBUG] HandleCallback: Authentication disabled, redirecting to home.")
			}
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] HandleCallback: Processing Azure AD callback.")
		}

		idToken, accessToken, err := auth.HandleCallback(r.Context(), r)
		if err != nil {
			log.Printf("Error handling authentication callback: %v", err)
			http.Error(w, fmt.Sprintf("Authentication error: %v", err), http.StatusInternalServerError)
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] HandleCallback: ID Token and Access Token successfully retrieved.")
		}

		claims, err := auth.GetUserClaims(idToken)
		if err != nil {
			log.Printf("Error extracting base claims: %v", err)
			http.Error(w, "Error processing user data", http.StatusInternalServerError)
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleCallback: Base claims extracted from ID Token for user: %s", claims.Email)
		}

		graphGroupIDs, graphGroupNames, err := auth.GetUserGroupsFromGraph(r.Context(), accessToken)
		if err != nil {
			log.Printf("Error getting user groups from Graph: %v", err)
			http.Error(w, fmt.Sprintf("Error retrieving user groups: %v", err), http.StatusInternalServerError)
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleCallback: User group IDs retrieved from Microsoft Graph: %v", graphGroupIDs)
			log.Printf("[DEBUG] HandleCallback: User group Names retrieved from Microsoft Graph: %v", graphGroupNames)
		}

		claims.Groups = graphGroupIDs
		claims.GroupNames = graphGroupNames // Salva i nomi dei gruppi nei claims
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleCallback: User claims updated with Graph groups. Final claims groups (IDs): %v", claims.Groups)
			log.Printf("[DEBUG] HandleCallback: User claims updated with Graph groups. Final claims groups (Names): %v", claims.GroupNames)
		}

		if !auth.IsUserAuthorized(claims, appConfig) {
			log.Printf("User not authorized at application level during request: %s", claims.Email)
			http.Error(w, "Access denied: User not authorized to use the application", http.StatusForbidden)
			return
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("[DEBUG] HandleCallback: User '%s' is authorized at application level.", claims.Email)
		}

		secure := false
		if r.Header.Get("X-Forwarded-Proto") == "https" {
			secure = true
		}

		claimsJSON, _ := json.Marshal(claims)
		cookie := &http.Cookie{
			Name:     "user_claims",
			Value:    url.QueryEscape(string(claimsJSON)),
			Path:     "/",
			Expires:  time.Now().Add(24 * time.Hour),
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		}
		http.SetCookie(w, cookie)
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Println("[DEBUG] HandleCallback: User claims stored in cookie.")
		}

		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Authentication successful for user: %s", claims.Email)
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("User %s authorized with groups (IDs): %v", claims.Email, claims.Groups)
				log.Printf("User %s authorized with groups (Names): %v", claims.Email, claims.GroupNames)
			}
		}
		http.Redirect(w, r, "/", http.StatusFound)
	}
}
