package httpapi

import "net/http"

// writeError writes a JSON error envelope: {"error": code, "message": msg}.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
