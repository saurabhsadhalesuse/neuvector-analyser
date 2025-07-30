// main.go
package main

import (
	"compress/gzip" // Correct standard library package
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

// Global variable to store the parsed data
var neuvectorData map[string]interface{}
var dataMutex sync.RWMutex // Mutex to protect access to neuvectorData

const supportBundlePath = "nvsupport_20250726184136.json.gz"
const frontendDir = "frontend" // Directory where your HTML/JS/CSS files will reside

func main() {
	// Added a version log to confirm the running backend
	log.Println("[INFO] NeuVector Support Bundle Viewer Backend (v1.2) starting...") // Updated version for clarity

	// Load the data once when the server starts
	if !loadData() {
		log.Fatalf("Failed to load support bundle data from %s. Exiting.", supportBundlePath)
	}

	// Serve static files from the "frontend" directory
	fs := http.FileServer(http.Dir(frontendDir))
	http.Handle("/", fs) // Serves index.html by default if present

	// API endpoint to get all top-level keys (with optional filtering)
	http.HandleFunc("/api/keys", getKeysHandler)
	// API endpoint to get data for a specific key
	http.HandleFunc("/api/data/", getDataHandler)

	port := ":8080"
	log.Printf("[INFO] Server listening on port %s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

// loadData decompresses the gzipped JSON file and parses it into neuvectorData.
func loadData() bool {
	log.Printf("[INFO] Loading data from %s...", supportBundlePath)

	// Open the gzipped file
	gzipFile, err := os.Open(supportBundlePath)
	if err != nil {
		log.Printf("[ERROR] Error opening gzipped file: %v\n", err)
		return false
	}
	defer gzipFile.Close()

	// Create a gzip reader
	gzr, err := gzip.NewReader(gzipFile)
	if err != nil {
		log.Printf("[ERROR] Error creating gzip reader: %v\n", err)
		return false
	}
	defer gzr.Close()

	// Read the decompressed data
	decompressedData, err := ioutil.ReadAll(gzr)
	if err != nil {
		log.Printf("[ERROR] Error reading decompressed data: %v\n", err)
		return false
	}
	log.Printf("[INFO] Successfully decompressed. Content size: %d characters.\n", len(decompressedData))

	// Unmarshal the JSON data
	dataMutex.Lock() // Protect global data during write
	err = json.Unmarshal(decompressedData, &neuvectorData)
	dataMutex.Unlock()
	if err != nil {
		log.Printf("[ERROR] Error parsing JSON data: %v\n", err)
		return false
	}
	log.Println("[INFO] Successfully parsed content as JSON.")
	return true
}

// getKeysHandler returns a list of top-level keys, optionally filtered by a query parameter.
func getKeysHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	dataMutex.RLock() // Protect global data during read
	defer dataMutex.RUnlock()

	if neuvectorData == nil || len(neuvectorData) == 0 {
		http.Error(w, `{"error": "Data not loaded."}`, http.StatusInternalServerError)
		return
	}

	allKeys := make([]string, 0, len(neuvectorData))
	for key := range neuvectorData {
		allKeys = append(allKeys, key)
	}

	// Implement filtering based on 'q' query parameter
	query := r.URL.Query().Get("q")
	if query != "" {
		filteredKeys := []string{}
		lowerQuery := strings.ToLower(query)
		for _, key := range allKeys {
			if strings.Contains(strings.ToLower(key), lowerQuery) {
				filteredKeys = append(filteredKeys, key)
			}
		}
		allKeys = filteredKeys
	}

	json.NewEncoder(w).Encode(allKeys)
}

// getDataHandler returns the JSON content for a specific key, with filtering for /v1/group, /v1/domain, and /v1/host.
func getDataHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	dataMutex.RLock() // Protect global data during read
	defer dataMutex.RUnlock()

	key := strings.TrimPrefix(r.URL.Path, "/api/data/")
	key, err := decodePath(key)
	if err != nil {
		log.Printf("[ERROR] Error decoding key path '%s': %v", r.URL.Path, err)
		http.Error(w, fmt.Sprintf(`{"error": "Invalid key path: %v"}`, err), http.StatusBadRequest)
		return
	}

	log.Printf("[INFO] Processing request for key: '%s'", key)

	if neuvectorData == nil || len(neuvectorData) == 0 {
		http.Error(w, `{"error": "Data not loaded."}`, http.StatusInternalServerError)
		return
	}

	val, ok := neuvectorData[key]
	if !ok {
		http.Error(w, fmt.Sprintf(`{"error": "Key '%s' not found."}`, key), http.StatusNotFound)
		return
	}

	// --- Special handling for /v1/group with filters ---
	if key == "/v1/group" {
		if valMap, isMap := val.(map[string]interface{}); isMap {
			if groupsIface, hasGroupsKey := valMap["groups"]; hasGroupsKey {
				if groups, isArray := groupsIface.([]interface{}); isArray {
					filteredGroups := []interface{}{}

					// Get filter parameters from query string
					filterZeroDriftStr := r.URL.Query().Get("zero_drift")
					filterDomain := r.URL.Query().Get("domain") // Keep original case for domain filter
					filterPolicyMode := strings.ToLower(r.URL.Query().Get("policy_mode"))

					for _, groupIface := range groups {
						group := groupIface.(map[string]interface{})

						groupName, _ := group["name"].(string)

						// --- Filtering logic ---
						if strings.HasPrefix(groupName, "_") { // Filter out groups starting with '_'
							continue
						}

						if filterZeroDriftStr != "" {
							zeroDriftEnabled, hasZeroDrift := group["zero_drift_enabled"].(bool)
							if !hasZeroDrift {
								zeroDriftEnabled = false
							}
							if filterZeroDriftStr == "true" && !zeroDriftEnabled {
								continue
							}
							if filterZeroDriftStr == "false" && zeroDriftEnabled {
								continue
							}
						}

						if filterDomain != "" {
							groupDomain, hasDomain := group["domain"].(string)
							if !hasDomain {
								groupDomain = ""
							}
							lowerGroupDomain := strings.ToLower(groupDomain)
							if !strings.Contains(lowerGroupDomain, strings.ToLower(filterDomain)) {
								continue
							}
						}

						if filterPolicyMode != "" {
							groupPolicyMode, hasPolicyMode := group["policy_mode"].(string)
							if !hasPolicyMode {
								groupPolicyMode = ""
							}
							lowerGroupPolicyMode := strings.ToLower(groupPolicyMode)
							if lowerGroupPolicyMode != filterPolicyMode {
								continue
							}
						}
						// --- End Filtering logic ---

						filteredGroups = append(filteredGroups, group)
					}
					json.NewEncoder(w).Encode(filteredGroups)
					return
				} else {
					log.Printf("[ERROR] Expected 'groups' key to be an array for /v1/group, but got %T. Keys found: %v", groupsIface, getMapKeys(valMap))
				}
			} else {
				log.Printf("[ERROR] Key '/v1/group' data is a map, but does not contain a 'groups' key. Keys found: %v", getMapKeys(valMap))
			}
		} else {
			log.Printf("[ERROR] Expected '/v1/group' data to be a map, but got %T. Cannot apply group filters.", val)
		}
	} else if key == "/v1/scan/platform" { // Handle /v1/scan/platform
		if valMap, isMap := val.(map[string]interface{}); isMap {
			if platformsIface, hasPlatformsKey := valMap["platforms"]; hasPlatformsKey {
				if platforms, isArray := platformsIface.([]interface{}); isArray {
					formattedPlatforms := []map[string]interface{}{}
					for _, platformIface := range platforms {
						if platformMap, isPlatformMap := platformIface.(map[string]interface{}); isPlatformMap {
							newPlatform := make(map[string]interface{})
							newPlatform["platform"] = platformMap["platform"]
							newPlatform["status"] = platformMap["status"]

							versionToUse := ""
							if platformName, ok := platformMap["platform"].(string); ok {
								if strings.Contains(strings.ToLower(platformName), "openshift") {
									if ov, ok := platformMap["openshift_version"].(string); ok {
										versionToUse = ov
									}
								} else {
									if kv, ok := platformMap["kube_version"].(string); ok {
										versionToUse = kv
									}
								}
							} else {
								if v, ok := platformMap["version"].(string); ok {
									versionToUse = v
								}
							}
							newPlatform["version"] = versionToUse

							if scanSummaryIface, hasScanSummary := platformMap["scan_summary"]; hasScanSummary {
								if scanSummaryMap, isScanSummaryMap := scanSummaryIface.(map[string]interface{}); isScanSummaryMap {
									newPlatform["high"] = scanSummaryMap["high"]
									newPlatform["medium"] = scanSummaryMap["medium"]
									newPlatform["scanned_at"] = scanSummaryMap["scanned_at"]
								}
							}
							formattedPlatforms = append(formattedPlatforms, newPlatform)
						}
					}
					json.NewEncoder(w).Encode(formattedPlatforms)
					return
				} else {
					log.Printf("[ERROR] Expected 'platforms' key to be an array for /v1/scan/platform, but got %T", platformsIface)
				}
			} else {
				log.Printf("[ERROR] Key '/v1/scan/platform' data is a map, but does not contain a 'platforms' key. Keys found: %v", getMapKeys(valMap))
			}
		} else {
			log.Printf("[ERROR] Expected '/v1/scan/platform' data to be a map, but got %T", val)
		}
		http.Error(w, `{"error": "Failed to process platform data."}`, http.StatusInternalServerError)
		return
	} else if key == "/v1/domain" { // Handle /v1/domain for Namespaces
		if valMap, isMap := val.(map[string]interface{}); isMap {
			if domainsIface, hasDomainsKey := valMap["domains"]; hasDomainsKey {
				if domains, isArray := domainsIface.([]interface{}); isArray {
					formattedDomains := []map[string]interface{}{}

					filterName := r.URL.Query().Get("domain") // Get the filter parameter for domain name

					for _, domainIface := range domains {
						if domainMap, isDomainMap := domainIface.(map[string]interface{}); isDomainMap {
							domainName, _ := domainMap["name"].(string)

							// --- Filter out domains starting with '_' ---
							if strings.HasPrefix(domainName, "_") {
								continue // Skip domains starting with '_'
							}
							// --- End Filter out domains starting with '_' ---

							// --- Apply name filter if present ---
							if filterName != "" {
								if !strings.Contains(strings.ToLower(domainName), strings.ToLower(filterName)) {
									continue // Skip if domain name doesn't match filter
								}
							}
							// --- End Apply name filter ---

							newDomain := make(map[string]interface{})
							newDomain["name"] = domainMap["name"]
							newDomain["workloads"] = domainMap["workloads"]
							newDomain["running_workloads"] = domainMap["running_workloads"]
							newDomain["running_pods"] = domainMap["running_pods"]
							newDomain["services"] = domainMap["services"]

							formattedDomains = append(formattedDomains, newDomain)
						}
					}
					json.NewEncoder(w).Encode(formattedDomains)
					return
				} else {
					log.Printf("[ERROR] Expected 'domains' key to be an array for /v1/domain, but got %T", domainsIface)
				}
			} else {
				log.Printf("[ERROR] Key '/v1/domain' data is a map, but does not contain a 'domains' key. Keys found: %v", getMapKeys(valMap))
			}
		} else {
			log.Printf("[ERROR] Expected '/v1/domain' data to be a map, but got %T", val)
		}
		http.Error(w, `{"error": "Failed to process domain data."}`, http.StatusInternalServerError)
		return
	} else if key == "/v1/host" { // Handle /v1/host for Nodes
		if valMap, isMap := val.(map[string]interface{}); isMap {
			if hostsIface, hasHostsKey := valMap["hosts"]; hasHostsKey { // Assuming "hosts" key contains the array
				if hosts, isArray := hostsIface.([]interface{}); isArray {
					formattedHosts := []map[string]interface{}{}

					filterName := r.URL.Query().Get("domain") // Reusing 'domain' filter param for host 'name'

					for _, hostIface := range hosts {
						if hostMap, isHostMap := hostIface.(map[string]interface{}); isHostMap {
							hostName, _ := hostMap["name"].(string)

							// Apply name filter if present
							if filterName != "" {
								if !strings.Contains(strings.ToLower(hostName), strings.ToLower(filterName)) {
									continue // Skip if host name doesn't match filter
								}
							}

							newHost := make(map[string]interface{})
							newHost["name"] = hostMap["name"]
							newHost["state"] = hostMap["state"]
							newHost["os"] = hostMap["os"]
							newHost["platform"] = hostMap["platform"]
							newHost["containers"] = hostMap["containers"] // Assuming this is a direct count or array

							if scanSummaryIface, hasScanSummary := hostMap["scan_summary"]; hasScanSummary {
								if scanSummaryMap, isScanSummaryMap := scanSummaryIface.(map[string]interface{}); isScanSummaryMap {
									newHost["scan_status"] = scanSummaryMap["status"] // Map scan_summary.status to Scan Status
									newHost["high"] = scanSummaryMap["high"]
									newHost["medium"] = scanSummaryMap["medium"]
									newHost["scanned_at"] = scanSummaryMap["scanned_at"]
								}
							}
							formattedHosts = append(formattedHosts, newHost)
						}
					}
					json.NewEncoder(w).Encode(formattedHosts)
					return
				} else {
					log.Printf("[ERROR] Expected 'hosts' key to be an array for /v1/host, but got %T", hostsIface)
				}
			} else {
				log.Printf("[ERROR] Key '/v1/host' data is a map, but does not contain a 'hosts' key. Keys found: %v", getMapKeys(valMap))
			}
		} else {
			log.Printf("[ERROR] Expected '/v1/host' data to be a map, but got %T", val)
		}
		http.Error(w, `{"error": "Failed to process host data."}`, http.StatusInternalServerError)
		return
	}

	// Default behavior for other keys
	json.NewEncoder(w).Encode(val)
}

// decodePath decodes URL-encoded path segments.
func decodePath(path string) (string, error) {
	decoded := strings.ReplaceAll(path, "%2F", "/")
	return decoded, nil
}

// getMapKeys extracts keys from a map[string]interface{} for logging
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
