// Handle coordinates form submission
document.addEventListener('DOMContentLoaded', function() {
    const coordinatesForm = document.getElementById('coordinates-form');
    if (coordinatesForm) {
        coordinatesForm.addEventListener('submit', function(e) {
            e.preventDefault();
            
            const longitude = parseFloat(document.getElementById('longitude').value);
            const latitude = parseFloat(document.getElementById('latitude').value);
            
            if (isNaN(longitude) || isNaN(latitude)) {
                showError('Please enter valid numerical coordinates');
                return;
            }
            
            if (longitude < -180 || longitude > 180) {
                showError('Longitude must be between -180 and 180');
                return;
            }
            
            if (latitude < -90 || latitude > 90) {
                showError('Latitude must be between -90 and 90');
                return;
            }
            
            findNearestCity(longitude, latitude);
        });
    }
});

// Find nearest city function
function findNearestCity(longitude, latitude) {
    const resultsDiv = document.getElementById('coordinates-results');
    const resultDiv = document.getElementById('city-result');
    
    // Show loading
    resultDiv.innerHTML = '<div style="color: var(--dracula-yellow);">🔍 Searching for nearest city...</div>';
    resultsDiv.style.display = 'block';
    
    // Make API call
    fetch(`/api/v1/cities/coordinates?longitude=${longitude}&latitude=${latitude}`)
        .then(response => {
            if (!response.ok) {
                throw new Error('Failed to find nearest city');
            }
            return response.json();
        })
        .then(data => {
            displayCityResult(data, longitude, latitude);
        })
        .catch(error => {
            showError('Error finding nearest city: ' + error.message);
            resultsDiv.style.display = 'none';
        });
}

// Display city result
function displayCityResult(data, searchLon, searchLat) {
    const resultDiv = document.getElementById('city-result');
    const city = data.city;
    const distance = data.distance;
    
    resultDiv.innerHTML = `
        <div class="city-item" style="border-left-color: var(--dracula-green);">
            <div style="display: grid; gap: 1rem;">
                <div>
                    <div class="city-name" style="font-size: 1.3rem;">${city.name}</div>
                    <div class="city-country" style="font-size: 1.1rem;">${city.country}</div>
                </div>
                
                <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; color: var(--dracula-comment);">
                    <div>
                        <strong style="color: var(--dracula-cyan);">Your Coordinates:</strong><br>
                        Lat: ${searchLat}<br>
                        Lon: ${searchLon}
                    </div>
                    <div>
                        <strong style="color: var(--dracula-cyan);">City Coordinates:</strong><br>
                        Lat: ${city.coord.lat}<br>
                        Lon: ${city.coord.lon}
                    </div>
                </div>
                
                <div style="text-align: center; padding: 1rem; background: var(--dracula-current-line); border-radius: 6px;">
                    <div style="font-size: 1.5rem; color: var(--dracula-green); font-weight: bold;">
                        📏 ${distance} km away
                    </div>
                    <div style="color: var(--dracula-comment); margin-top: 0.5rem;">
                        City ID: ${city.id} (OpenWeatherMap compatible)
                    </div>
                </div>
            </div>
        </div>
    `;
}

// Set coordinates helper
function setCoordinates(longitude, latitude) {
    document.getElementById('longitude').value = longitude;
    document.getElementById('latitude').value = latitude;
}

// Get user location
function getUserLocation() {
    const statusDiv = document.getElementById('location-status');
    const button = document.getElementById('get-location-btn');
    
    if (!navigator.geolocation) {
        showError('Geolocation is not supported by this browser');
        return;
    }
    
    // Show loading state
    button.textContent = '📍 Getting Location...';
    button.disabled = true;
    statusDiv.style.display = 'block';
    statusDiv.innerHTML = '<div style="color: var(--dracula-yellow);">🌍 Requesting your location...</div>';
    
    navigator.geolocation.getCurrentPosition(
        function(position) {
            const latitude = position.coords.latitude;
            const longitude = position.coords.longitude;
            const accuracy = position.coords.accuracy;
            
            // Set coordinates in form
            setCoordinates(longitude, latitude);
            
            // Show success status
            statusDiv.innerHTML = `
                <div class="success">
                    ✅ Location found! Accuracy: ±${Math.round(accuracy)}m<br>
                    Coordinates automatically filled below.
                </div>
            `;
            
            // Auto-find nearest city
            findNearestCity(longitude, latitude);
            
            // Reset button
            button.textContent = '📍 Use My Current Location';
            button.disabled = false;
        },
        function(error) {
            let errorMessage = 'Unable to get your location';
            
            switch(error.code) {
                case error.PERMISSION_DENIED:
                    errorMessage = 'Location access denied by user';
                    break;
                case error.POSITION_UNAVAILABLE:
                    errorMessage = 'Location information unavailable';
                    break;
                case error.TIMEOUT:
                    errorMessage = 'Location request timed out';
                    break;
            }
            
            statusDiv.innerHTML = `<div class="error">❌ ${errorMessage}</div>`;
            
            // Reset button
            button.textContent = '📍 Use My Current Location';
            button.disabled = false;
        },
        {
            enableHighAccuracy: true,
            timeout: 10000,
            maximumAge: 60000
        }
    );
}

// Show error helper
function showError(message) {
    const statusDiv = document.getElementById('location-status');
    statusDiv.style.display = 'block';
    statusDiv.innerHTML = `<div class="error">❌ ${message}</div>`;
}