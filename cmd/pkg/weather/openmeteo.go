package weather

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	openMeteoBaseURL = "https://api.open-meteo.com/v1"
	nominatimBaseURL = "https://nominatim.openstreetmap.org"
)

// Cache entry for coordinates
type coordCacheEntry struct {
	coords    Coordinates
	expiresAt time.Time
}

var (
	// Cache for postal code -> coordinates mapping
	coordCache = make(map[string]coordCacheEntry)
	cacheLock  sync.RWMutex
	// Cache duration of 24 hours
	cacheDuration = 24 * time.Hour
)

type Coordinates struct {
	Latitude  float64
	Longitude float64
}

// NominatimResponse represents the response from Nominatim geocoding API
type NominatimResponse struct {
	Lat float64 `json:"lat,string"`
	Lon float64 `json:"lon,string"`
}

// getCoordinatesFromPostalCode converts a postal code to coordinates using Nominatim
func getCoordinatesFromPostalCode(postalCode string) (Coordinates, error) {
	// Check cache first
	cacheLock.RLock()
	if entry, ok := coordCache[postalCode]; ok && time.Now().Before(entry.expiresAt) {
		cacheLock.RUnlock()
		return entry.coords, nil
	}
	cacheLock.RUnlock()

	// Make request to Nominatim
	queryURL := fmt.Sprintf("%s/search?format=json&postalcode=%s&country=us&limit=1",
		nominatimBaseURL, url.QueryEscape(postalCode))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(queryURL)
	if err != nil {
		return Coordinates{}, fmt.Errorf("failed to fetch coordinates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Coordinates{}, fmt.Errorf("geocoding API returned status %d", resp.StatusCode)
	}

	var results []NominatimResponse
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return Coordinates{}, fmt.Errorf("failed to decode geocoding response: %w", err)
	}

	if len(results) == 0 {
		return Coordinates{}, fmt.Errorf("no coordinates found for postal code %s", postalCode)
	}

	coords := Coordinates{Latitude: results[0].Lat, Longitude: results[0].Lon}

	// Update cache
	cacheLock.Lock()
	coordCache[postalCode] = coordCacheEntry{
		coords:    coords,
		expiresAt: time.Now().Add(cacheDuration),
	}
	cacheLock.Unlock()

	return coords, nil
}

// FetchWeatherByPostalCode gets weather data for the given postal code
func FetchWeatherByPostalCode(postalCode string) (*OpenMeteoResponse, error) {
	coords, err := getCoordinatesFromPostalCode(postalCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get coordinates for postal code: %w", err)
	}

	return FetchWeather(coords)
}

type OpenMeteoResponse struct {
	Daily struct {
		Time             []string  `json:"time"`
		Temperature2mMax []float64 `json:"temperature_2m_max"`
		Temperature2mMin []float64 `json:"temperature_2m_min"`
		WeatherCode      []int     `json:"weathercode"`
		PrecipitationMax []int     `json:"precipitation_probability_max"`
	} `json:"daily"`
}

// FetchWeather gets weather data for the given coordinates
func FetchWeather(coords Coordinates) (*OpenMeteoResponse, error) {
	url := fmt.Sprintf("%s/forecast?latitude=%.4f&longitude=%.4f&daily=weathercode,temperature_2m_max,temperature_2m_min,precipitation_probability_max&timezone=auto",
		openMeteoBaseURL, coords.Latitude, coords.Longitude)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch weather: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather API returned status %d", resp.StatusCode)
	}

	var result OpenMeteoResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode weather response: %w", err)
	}

	return &result, nil
}

// ConvertWeatherCode maps OpenMeteo weather codes to thermostat status IDs
func ConvertWeatherCode(code int) int {
	switch code {
	case 95, 96, 99: // Thunderstorm
		return 1
	case 66, 67: // Sleet
		return 2
	case 68, 69, 70: // Rain and sleet
		return 3
	case 71, 73, 75: // Wintry mix
		return 4
	case 77: // Rain and snow
		return 5
	case 85, 86: // Snow
		return 6
	case 56, 57: // Freezing rain
		return 7
	case 51, 53, 55, 61, 63, 65, 80, 81, 82: // Rain
		return 8
	case 45, 48: // Fog
		return 10
	case 3: // Cloudy
		return 11
	case 1: // Partly cloudy
		return 12
	case 2: // Mostly cloudy
		return 13
	case 0: // Clear
		return 14
	default:
		return 14 // Default to clear if unknown
	}
}
