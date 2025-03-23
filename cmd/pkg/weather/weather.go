package weather

import (
	"fmt"
	"regexp"
)

var postalCodeRegex = regexp.MustCompile(`^\d{5}(-\d{4})?$`)

// ValidatePostalCode checks if a postal code is in valid US format (5 digits or 5+4)
func ValidatePostalCode(postalCode string) error {
	if !postalCodeRegex.MatchString(postalCode) {
		return fmt.Errorf("invalid postal code format: %s (must be 5 digits or 5+4 format)", postalCode)
	}
	return nil
}

// GetWeatherDataByPostalCode fetches weather data for a specific postal code
func GetWeatherDataByPostalCode(postalCode string) (*OpenMeteoResponse, error) {
	if err := ValidatePostalCode(postalCode); err != nil {
		return nil, err
	}
	return FetchWeatherByPostalCode(postalCode)
}

// GetPrecipitationProbability returns formatted precipitation probability (0-10)
// based on the maximum precipitation probability
func GetPrecipitationProbability(maxPrecipitationProbability int) int {
	return maxPrecipitationProbability / 10
}

// GetForecastDataByPostalCode returns formatted forecast data for all days for a specific postal code
func GetForecastDataByPostalCode(postalCode string) ([]ForecastDay, error) {
	if err := ValidatePostalCode(postalCode); err != nil {
		return nil, err
	}
	resp, err := GetWeatherDataByPostalCode(postalCode)
	if err != nil {
		return nil, err
	}

	var days []ForecastDay
	for i := range resp.Daily.Time {
		days = append(days, ForecastDay{
			MaxTemp:       int(resp.Daily.Temperature2mMax[i]),
			MinTemp:       int(resp.Daily.Temperature2mMin[i]),
			StatusID:      ConvertWeatherCode(resp.Daily.WeatherCode[i]),
			Precipitation: GetPrecipitationProbability(resp.Daily.PrecipitationMax[i]),
		})
	}

	return days, nil
}

// ForecastDay represents weather data for a single day
type ForecastDay struct {
	MaxTemp       int
	MinTemp       int
	StatusID      int
	Precipitation int
}
