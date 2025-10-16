// FORBIDDEN - Never use these:
// ❌ alert()
// ❌ confirm()
// ❌ prompt()
// ❌ document.write()

// REQUIRED - Professional UI functions per SPEC
class UI {
  // Modal Management
  static showModal(options) {
    const {
      title = 'Modal',
      message = '',
      confirmText = 'OK',
      cancelText = 'Cancel',
      confirmClass = 'btn-primary',
      showCancel = true,
      onConfirm = () => {},
      onCancel = () => {}
    } = options;

    return new Promise((resolve) => {
      const container = document.getElementById('modal-container');

      const modal = document.createElement('div');
      modal.className = 'modal';
      modal.innerHTML = `
        <div class="modal-backdrop"></div>
        <div class="modal-content">
          <div class="modal-header">
            <h2 class="modal-title">${title}</h2>
            <button class="modal-close" aria-label="Close">×</button>
          </div>
          <div class="modal-body">
            <p>${message}</p>
          </div>
          <div class="modal-footer">
            ${showCancel ? `<button class="btn btn-secondary modal-cancel">${cancelText}</button>` : ''}
            <button class="btn ${confirmClass} modal-confirm">${confirmText}</button>
          </div>
        </div>
      `;

      container.appendChild(modal);
      container.style.display = 'block';

      // Focus trap
      const focusableElements = modal.querySelectorAll('button');
      const firstFocusable = focusableElements[0];
      const lastFocusable = focusableElements[focusableElements.length - 1];
      firstFocusable.focus();

      const closeModal = (result) => {
        modal.style.animation = 'fadeOut 300ms ease';
        setTimeout(() => {
          modal.remove();
          if (container.children.length === 0) {
            container.style.display = 'none';
          }
        }, 300);
        resolve(result);
      };

      // Event listeners
      modal.querySelector('.modal-close').addEventListener('click', () => {
        onCancel();
        closeModal(false);
      });

      modal.querySelector('.modal-backdrop').addEventListener('click', () => {
        onCancel();
        closeModal(false);
      });

      if (showCancel) {
        modal.querySelector('.modal-cancel').addEventListener('click', () => {
          onCancel();
          closeModal(false);
        });
      }

      modal.querySelector('.modal-confirm').addEventListener('click', () => {
        onConfirm();
        closeModal(true);
      });

      // ESC key to close
      const escHandler = (e) => {
        if (e.key === 'Escape') {
          onCancel();
          closeModal(false);
          document.removeEventListener('keydown', escHandler);
        }
      };
      document.addEventListener('keydown', escHandler);

      // Tab trap
      modal.addEventListener('keydown', (e) => {
        if (e.key === 'Tab') {
          if (e.shiftKey) {
            if (document.activeElement === firstFocusable) {
              e.preventDefault();
              lastFocusable.focus();
            }
          } else {
            if (document.activeElement === lastFocusable) {
              e.preventDefault();
              firstFocusable.focus();
            }
          }
        }
      });
    });
  }

  // Toast Notifications
  static showToast(message, type = 'info', duration = 5000) {
    const container = document.getElementById('toast-container');

    const iconMap = {
      success: '✅',
      error: '❌',
      warning: '⚠️',
      info: 'ℹ️'
    };

    const toast = document.createElement('div');
    toast.className = `toast ${type}`;
    toast.innerHTML = `
      <span class="toast-icon">${iconMap[type] || iconMap.info}</span>
      <div class="toast-content">${message}</div>
      <button class="toast-close" aria-label="Close">×</button>
    `;

    container.appendChild(toast);

    // Close button
    toast.querySelector('.toast-close').addEventListener('click', () => {
      removeToast(toast);
    });

    // Auto-dismiss
    const removeToast = (element) => {
      element.style.animation = 'slideOut 300ms ease';
      setTimeout(() => element.remove(), 300);
    };

    if (duration > 0) {
      setTimeout(() => removeToast(toast), duration);
    }

    return toast;
  }

  // Confirmation Dialogs
  static confirm(options) {
    return UI.showModal({
      ...options,
      showCancel: true
    });
  }

  // Timezone Conversion (placeholder for future implementation)
  static convertTimestamps() {
    // Find all <time> elements
    // Convert to user's local timezone
    // Update display text
    // Keep original in data attribute
    const timeElements = document.querySelectorAll('time[data-unix]');
    timeElements.forEach(el => {
      const unix = parseInt(el.getAttribute('data-unix'));
      if (!isNaN(unix)) {
        const date = new Date(unix * 1000);
        el.textContent = date.toLocaleString();
      }
    });
  }

  // Relative Time Updates (placeholder for future implementation)
  static updateRelativeTimes() {
    // Find all [data-format="relative"]
    // Update "2 minutes ago" → "3 minutes ago"
    // Run every minute
    const relativeElements = document.querySelectorAll('[data-format="relative"]');
    relativeElements.forEach(el => {
      const timestamp = parseInt(el.getAttribute('data-timestamp'));
      if (!isNaN(timestamp)) {
        const diff = Date.now() - timestamp;
        const minutes = Math.floor(diff / 60000);
        const hours = Math.floor(minutes / 60);
        const days = Math.floor(hours / 24);

        if (days > 0) {
          el.textContent = `${days} day${days > 1 ? 's' : ''} ago`;
        } else if (hours > 0) {
          el.textContent = `${hours} hour${hours > 1 ? 's' : ''} ago`;
        } else if (minutes > 0) {
          el.textContent = `${minutes} minute${minutes > 1 ? 's' : ''} ago`;
        } else {
          el.textContent = 'just now';
        }
      }
    });
  }
}

// API Client
class CityAPI {
  static async search(query) {
    const response = await fetch(`/api/v1/cities/search?q=${encodeURIComponent(query)}`);
    return await response.json();
  }

  static async getCities(limit = 10, offset = 0) {
    const response = await fetch(`/api/v1/cities?limit=${limit}&offset=${offset}`);
    return await response.json();
  }

  static async getStats() {
    const response = await fetch('/api/v1/cities?limit=1');
    return await response.json();
  }
}

// Search functionality
let searchTimeout;
const searchInput = document.getElementById('search-input');
const searchBtn = document.getElementById('search-btn');
const searchResults = document.getElementById('search-results');
const searchError = document.getElementById('search-error');

async function performSearch() {
  const query = searchInput.value.trim();

  // Clear previous errors
  searchError.textContent = '';
  searchInput.classList.remove('error');

  if (!query) {
    searchResults.innerHTML = '';
    return;
  }

  if (query.length < 2) {
    searchError.textContent = 'Please enter at least 2 characters';
    searchInput.classList.add('error');
    UI.showToast('Please enter at least 2 characters', 'error', 3000);
    return;
  }

  // Show loading state
  searchBtn.classList.add('loading');
  searchResults.innerHTML = '<div class="loading-spinner"></div>';

  try {
    const result = await CityAPI.search(query);

    if (result.success && result.data.cities.length > 0) {
      displayResults(result.data.cities);
      UI.showToast(`Found ${result.data.count} cities`, 'success', 3000);
    } else {
      searchResults.innerHTML = '<p style="color: var(--text-secondary); text-align: center; padding: 2rem;">No cities found</p>';
      UI.showToast('No cities found', 'info', 3000);
    }
  } catch (error) {
    console.error('Search error:', error);
    searchResults.innerHTML = '<p style="color: var(--accent-danger); text-align: center; padding: 2rem;">Error searching cities</p>';
    UI.showToast('Error searching cities. Please try again.', 'error', 3000);
  } finally {
    searchBtn.classList.remove('loading');
  }
}

function displayResults(cities) {
  searchResults.innerHTML = cities.map(city => `
    <div class="city-card">
      <h3>${city.name}</h3>
      <p>Country: ${city.country}</p>
      <p class="coord">Coordinates: ${city.lat.toFixed(6)}, ${city.lon.toFixed(6)}</p>
    </div>
  `).join('');
}

// Event listeners
if (searchBtn) {
  searchBtn.addEventListener('click', performSearch);
}

if (searchInput) {
  searchInput.addEventListener('keyup', (e) => {
    if (e.key === 'Enter') {
      performSearch();
    } else {
      // Debounce search
      clearTimeout(searchTimeout);
      searchTimeout = setTimeout(() => {
        if (searchInput.value.length >= 2) {
          performSearch();
        }
      }, 500);
    }
  });
}

// Load statistics on page load
async function loadStats() {
  try {
    const stats = await CityAPI.getStats();
    if (stats.success) {
      const totalCitiesEl = document.getElementById('total-cities');
      const totalCountriesEl = document.getElementById('total-countries');

      if (totalCitiesEl) {
        totalCitiesEl.textContent = stats.data.total.toLocaleString();
      }

      if (totalCountriesEl) {
        // Note: This is a simplified version
        // In production, you'd want to query distinct countries from the backend
        totalCountriesEl.textContent = '190+';
      }
    }
  } catch (error) {
    console.error('Error loading stats:', error);
    const totalCitiesEl = document.getElementById('total-cities');
    const totalCountriesEl = document.getElementById('total-countries');
    if (totalCitiesEl) totalCitiesEl.textContent = 'Error';
    if (totalCountriesEl) totalCountriesEl.textContent = 'Error';
  }
}

// Theme toggle functionality
const themeToggle = document.getElementById('theme-toggle');
if (themeToggle) {
  themeToggle.addEventListener('click', (e) => {
    e.preventDefault();
    const currentTheme = document.body.getAttribute('data-theme');
    const newTheme = currentTheme === 'dark' ? 'light' : 'dark';
    document.body.setAttribute('data-theme', newTheme);
    localStorage.setItem('theme', newTheme);
    UI.showToast(`Switched to ${newTheme} theme`, 'success', 2000);
  });
}

// Load saved theme
const savedTheme = localStorage.getItem('theme');
if (savedTheme) {
  document.body.setAttribute('data-theme', savedTheme);
}

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
  loadStats();
  UI.convertTimestamps();

  // Update relative times every minute
  setInterval(UI.updateRelativeTimes, 60000);

  // Welcome toast
  UI.showToast('Welcome to CityList API! 🌍', 'success', 3000);
});

// Example usage of modal (for demonstration)
// UI.showModal({
//   title: 'Confirm Action',
//   message: 'Are you sure you want to proceed?',
//   confirmText: 'Yes, proceed',
//   cancelText: 'Cancel',
//   confirmClass: 'btn-primary',
//   onConfirm: () => console.log('Confirmed'),
//   onCancel: () => console.log('Cancelled')
// });
