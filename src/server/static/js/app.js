function citylistPath(path) {
    const prefix = document.querySelector('meta[name="base-url"]')?.content.replace(/\/$/, '') || '';
    return `${prefix}/${path.replace(/^\/+/, '')}`;
}

let clientMessages = {};

async function loadClientTranslations() {
    const language = document.documentElement.lang || 'en';
    const stylesheet = document.querySelector('link[rel="stylesheet"][href*="/static/css/common.css"]');
    let stamp = '';
    try {
        stamp = new URL(stylesheet.href, window.location.href).searchParams.get('v') || '';
    } catch {
        stamp = '';
    }
    const url = new URL(citylistPath(`/locales/${encodeURIComponent(language)}.json`), window.location.href);
    if (stamp) url.searchParams.set('v', stamp);
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 30000);
    try {
        const response = await fetch(url, { signal: controller.signal, cache: 'no-cache' });
        if (!response.ok) return {};
        return (await response.json()).messages || {};
    } catch {
        return {};
    } finally {
        window.clearTimeout(timeout);
    }
}

let clientTranslationsReady;
const fallbackClientTranslations = {
    install_app: 'Install Citylist',
    install_instructions: 'To install Citylist, use your browser menu and select Add to Home Screen.',
    update_available: 'A new version is available',
    update_available_dirty: 'Update available - reload when ready',
    update_now: 'Update Now',
    later: 'Later',
    dismiss: 'Dismiss',
    'pwa.page_unavailable': 'Page unavailable',
    'pwa.reload_page': 'Please reload this page.',
};

function clientText(key) {
    const value = key.includes('.')
        ? key.split('.').reduce((messages, part) => messages?.[part], clientMessages)
        : clientMessages.pwa?.[key];
    return value || fallbackClientTranslations[key] || key;
}

function clientPlural(key, count, values = {}) {
    const forms = key.split('.').reduce((messages, part) => messages?.[part], clientMessages);
    const category = new Intl.PluralRules(document.documentElement.lang || 'en').select(count);
    const message = forms?.[category] || forms?.other || key;
    return message.replace(/\{([^}]+)\}/g, (match, name) => values[name] ?? match);
}

function getClientTranslations() {
    if (!clientTranslationsReady) clientTranslationsReady = loadClientTranslations();
    return clientTranslationsReady;
}

async function showToast(message, type = 'info', duration = 3000) {
    clientMessages = await getClientTranslations();
    let container = document.getElementById('toast-container');
    if (!container) {
        container = document.createElement('div');
        container.id = 'toast-container';
        container.setAttribute('aria-label', clientText('pwa.notifications'));
        document.body.appendChild(container);
    }
    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;
    toast.setAttribute('role', 'alert');
    toast.setAttribute('aria-live', 'polite');
    const icon = document.createElement('span');
    icon.className = 'toast-icon';
    icon.setAttribute('aria-hidden', 'true');
    icon.textContent = type === 'error' ? '✗' : 'ℹ';
    const text = document.createElement('span');
    text.className = 'toast-message';
    text.textContent = message;
    const close = document.createElement('button');
    close.className = 'toast-close';
    close.type = 'button';
    close.setAttribute('aria-label', clientText('pwa.dismiss'));
    close.textContent = '×';
    close.addEventListener('click', () => toast.remove());
    toast.addEventListener('click', (event) => {
        if (event.target === toast) toast.remove();
    });
    toast.append(icon, text, close);
    container.prepend(toast);
    while (container.children.length > 5) container.lastElementChild.remove();
    if (duration > 0) {
        const timer = window.setTimeout(() => toast.remove(), duration);
        toast.addEventListener('mouseenter', () => window.clearTimeout(timer), { once: true });
    }
}

async function showToastForKey(key) {
    clientMessages = await getClientTranslations();
    await showToast(clientText(key), 'error', 0);
}

document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    document.getElementById('toast-container')?.firstElementChild?.remove();
});

const bootWatchdog = window.setTimeout(async () => {
    let recovered = false;
    try {
        recovered = sessionStorage.getItem('citylist_recovered') === 'true';
        if (!recovered) sessionStorage.setItem('citylist_recovered', 'true');
    } catch {
        recovered = true;
    }
    if (recovered) {
        clientMessages = await getClientTranslations();
        const title = clientText('pwa.page_unavailable');
        document.title = title;
        const main = document.createElement('main');
        const heading = document.createElement('h1');
        heading.textContent = title;
        const message = document.createElement('p');
        message.textContent = clientText('pwa.reload_page');
        main.append(heading, message);
        document.body.replaceChildren(main);
        return;
    }
    try {
        const registrations = await navigator.serviceWorker?.getRegistrations() || [];
        await Promise.all(registrations.map((registration) => registration.unregister()));
        const cacheNames = await caches.keys();
        await Promise.all(cacheNames.map((name) => caches.delete(name)));
    } catch {
        // Reload still gives the server a chance to deliver fresh HTML and assets.
    }
    window.location.reload();
}, 10000);

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

function readConsentCookie() {
    const match = document.cookie.match(/(?:^|;\s*)cookie_consent=([^;]+)/);
    if (!match) return null;
    try {
        return JSON.parse(decodeURIComponent(match[1].replace(/\+/g, ' ')));
    } catch {
        return null;
    }
}

function writeConsentCookie(consent) {
    const secure = window.location.protocol === 'https:' ? '; Secure' : '';
    document.cookie = `cookie_consent=${encodeURIComponent(JSON.stringify(consent))}; path=/; max-age=31536000; SameSite=Lax${secure}`;
}

function clearCookie(name) {
    const secure = window.location.protocol === 'https:' ? '; Secure' : '';
    document.cookie = `${name}=; path=/; max-age=0; SameSite=Lax${secure}`;
}

function clearPreferenceCookies() {
    clearCookie('theme');
    clearCookie('lang');
    document.cookie.split(';').forEach((entry) => {
        const name = entry.trim().split('=', 1)[0];
        if (name.startsWith('citylist_pref_')) clearCookie(name);
    });
}

function closeCookiePreferences() {
    const dialog = document.getElementById('cookie-preferences-modal');
    if (dialog?.open) dialog.close();
}

function saveAndApplyConsent(consent) {
    writeConsentCookie(consent);
    if (!consent.preferences) {
        clearPreferenceCookies();
    }
    document.getElementById('cookie-consent')?.remove();
    closeCookiePreferences();
}

function bindConsentControls() {
    document.querySelectorAll('[data-action="cookie-preferences"]').forEach((control) => {
        control.addEventListener('click', (event) => {
            const dialog = document.getElementById('cookie-preferences-modal');
            if (!dialog?.showModal) return;
            event.preventDefault();
            dialog.showModal();
        });
    });

    document.querySelectorAll('[data-action="close-cookie-preferences"]').forEach((control) => {
        control.addEventListener('click', closeCookiePreferences);
    });

    document.querySelectorAll('[data-consent-choice]').forEach((form) => {
        form.addEventListener('submit', (event) => {
            event.preventDefault();
            const choice = form.elements.choice.value;
            const accepted = choice === 'accept';
            saveAndApplyConsent({
                essential: true,
                preferences: accepted,
                analytics: accepted,
                timestamp: Math.floor(Date.now() / 1000),
            });
        });
    });

    document.querySelectorAll('[data-consent-save]').forEach((form) => {
        form.addEventListener('submit', (event) => {
            event.preventDefault();
            saveAndApplyConsent({
                essential: true,
                preferences: form.elements.preferences.checked,
                analytics: form.elements.analytics.checked,
                timestamp: Math.floor(Date.now() / 1000),
            });
        });
    });

    document.querySelectorAll('[data-ccpa-form]').forEach((form) => {
        form.addEventListener('submit', (event) => {
            const optOut = form.elements.choice.checked;
            if (!optOut) return;
            event.preventDefault();
            const secure = window.location.protocol === 'https:' ? '; Secure' : '';
            document.cookie = `ccpa_opt_out=true; path=/; max-age=31536000; SameSite=Lax${secure}`;
            saveAndApplyConsent({
                essential: true,
                preferences: false,
                analytics: false,
                timestamp: Math.floor(Date.now() / 1000),
            });
        });
    });

    const consent = readConsentCookie();
    if (!consent?.preferences) clearPreferenceCookies();
}

function formatNumber(num) {
    return num.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

function bindSearchPage() {
    const searchForm = document.getElementById('searchForm');
    if (!searchForm) return;
    const typeSelect = document.getElementById('searchType');
    const queryInput = document.getElementById('query');
    const queryLabel = document.getElementById('queryLabel');
    const queryHint = document.getElementById('queryHint');
    const cityFilters = document.getElementById('cityFilters');
    const submitLabel = document.getElementById('searchSubmitLabel');
    const loadingLabel = document.getElementById('loadingLabel');
    const loading = document.getElementById('loading');
    const results = document.getElementById('results');
    const resultsList = document.getElementById('resultsList');
    const resultCount = document.getElementById('resultCount');
    const emptyState = document.getElementById('emptyState');
    const emptyTitle = document.getElementById('emptyStateTitle');
    const emptyDescription = emptyState.querySelector('.empty-state-description');
    const setSearchType = () => {
        const isZIP = typeSelect.value === 'zip';
        queryLabel.textContent = isZIP ? searchForm.dataset.zipLabel : searchForm.dataset.cityLabel;
        queryInput.placeholder = isZIP ? searchForm.dataset.zipPlaceholder : searchForm.dataset.cityPlaceholder;
        queryHint.textContent = isZIP ? searchForm.dataset.zipHint : searchForm.dataset.cityHint;
        submitLabel.textContent = isZIP ? searchForm.dataset.zipSubmit : searchForm.dataset.citySubmit;
        loadingLabel.textContent = isZIP ? searchForm.dataset.zipLoading : searchForm.dataset.cityLoading;
        emptyTitle.textContent = isZIP ? searchForm.dataset.zipEmpty : searchForm.dataset.cityEmpty;
        cityFilters.hidden = isZIP;
        if (isZIP) {
            queryInput.removeAttribute('minlength');
            queryInput.maxLength = 5;
            queryInput.inputMode = 'numeric';
            queryInput.pattern = '[0-9]{5}';
        } else {
            queryInput.minLength = 2;
            queryInput.maxLength = 100;
            queryInput.removeAttribute('inputmode');
            queryInput.removeAttribute('pattern');
        }
    };
    typeSelect.addEventListener('change', setSearchType);
    setSearchType();
    searchForm.addEventListener('submit', async (event) => {
        event.preventDefault();
        const query = queryInput.value.trim();
        const isZIP = typeSelect.value === 'zip';
        const country = document.getElementById('country').value.trim().toUpperCase();
        const limit = document.getElementById('limit').value;
        if (isZIP && !/^\d{5}$/.test(query)) {
            emptyTitle.textContent = searchForm.dataset.zipError;
            emptyDescription.textContent = searchForm.dataset.zipErrorDetail;
            emptyState.classList.remove('is-hidden');
            results.classList.add('is-hidden');
            return;
        }
        loading.classList.remove('is-hidden');
        results.classList.add('is-hidden');
        emptyState.classList.add('is-hidden');
        try {
            const apiBase = document.querySelector('meta[name="api-base"]')?.content.replace(/\/$/, '') || citylistPath('/api/v1');
            let url;
            if (isZIP) {
                url = `${apiBase}/zipcodes/${encodeURIComponent(query)}`;
            } else {
                url = `${apiBase}/cities/search?q=${encodeURIComponent(query)}&limit=${encodeURIComponent(limit)}`;
                if (country) url += `&country=${encodeURIComponent(country)}`;
            }
            const response = await fetch(url);
            const payload = await response.json();
            if (!response.ok || !payload.ok) throw new Error(payload.message || searchForm.dataset.cityErrorDetail);
            if (!isZIP && !Array.isArray(payload.data)) throw new Error(searchForm.dataset.cityErrorDetail);
            loading.classList.add('is-hidden');
            const data = isZIP ? (payload.data ? [payload.data] : []) : payload.data;
            if (data.length === 0) {
                emptyTitle.textContent = isZIP ? searchForm.dataset.zipEmpty : searchForm.dataset.cityEmpty;
                emptyDescription.textContent = searchForm.dataset.cityErrorDetail;
                emptyState.classList.remove('is-hidden');
                return;
            }
            results.classList.remove('is-hidden');
            clientMessages = await getClientTranslations();
            resultCount.textContent = isZIP ? searchForm.dataset.zipLabel : clientPlural('plurals.cities_found', data.length, { count: data.length });
            resultsList.innerHTML = isZIP ? data.map((record) => `
                <article class="result-card">
                    <div class="result-card-header"><h3 class="result-card-title">${escapeHtml(record.city)}</h3><span class="result-card-badge">${escapeHtml(record.state)}</span></div>
                    <div class="result-card-body">
                        <div class="result-card-stat"><span class="result-card-stat-label">${escapeHtml(searchForm.dataset.zipLabel)}</span><span class="result-card-stat-value">${escapeHtml(record.zip)}</span></div>
                        <div class="result-card-stat"><span class="result-card-stat-label">${escapeHtml(searchForm.dataset.latitudeLabel)}</span><span class="result-card-stat-value">${Number(record.lat).toFixed(4)}</span></div>
                        <div class="result-card-stat"><span class="result-card-stat-label">${escapeHtml(searchForm.dataset.longitudeLabel)}</span><span class="result-card-stat-value">${Number(record.lon).toFixed(4)}</span></div>
                    </div>
                </article>`).join('') : data.map((city) => `
                <div class="result-card">
                    <div class="result-card-header"><h3 class="result-card-title">${escapeHtml(city.name)}</h3><span class="result-card-badge">${escapeHtml(city.country)}</span></div>
                    <div class="result-card-body">
                        <div class="result-card-stat"><span class="result-card-stat-label">${escapeHtml(clientText('results.city_id'))}</span><span class="result-card-stat-value">${city.id}</span></div>
                        <div class="result-card-stat"><span class="result-card-stat-label">${escapeHtml(clientText('results.latitude'))}</span><span class="result-card-stat-value">${city.coord.lat.toFixed(4)}</span></div>
                        <div class="result-card-stat"><span class="result-card-stat-label">${escapeHtml(clientText('results.longitude'))}</span><span class="result-card-stat-value">${city.coord.lon.toFixed(4)}</span></div>
                    </div>
                </div>`).join('');
        } catch (error) {
            loading.classList.add('is-hidden');
            emptyTitle.textContent = isZIP ? searchForm.dataset.zipEmpty : searchForm.dataset.cityError;
            emptyDescription.textContent = error.message || (isZIP ? searchForm.dataset.zipErrorDetail : searchForm.dataset.cityErrorDetail);
            emptyState.classList.remove('is-hidden');
        }
    });
    document.getElementById('query')?.focus();
}

function bindCoordinatesPage() {
    const coordForm = document.getElementById('coordForm');
    if (!coordForm) return;
    const useLocationBtn = document.getElementById('useLocation');
    const locationStatus = document.getElementById('locationStatus');
    const loading = document.getElementById('loading');
    const results = document.getElementById('results');
    const resultsList = document.getElementById('resultsList');
    const resultCount = document.getElementById('resultCount');
    const emptyState = document.getElementById('emptyState');
    useLocationBtn?.addEventListener('click', async () => {
        clientMessages = await getClientTranslations();
        if (!navigator.geolocation) {
            showToastForKey('coordinates.geolocation_unsupported');
            return;
        }
        locationStatus.style.display = 'flex';
        locationStatus.querySelector('.location-status-text').textContent = clientText('coordinates.getting_location');
        navigator.geolocation.getCurrentPosition((position) => {
            document.getElementById('lat').value = position.coords.latitude.toFixed(6);
            document.getElementById('lon').value = position.coords.longitude.toFixed(6);
            locationStatus.querySelector('.location-status-text').textContent = clientText('coordinates.location_found');
            setTimeout(() => { locationStatus.style.display = 'none'; }, 2000);
        }, (error) => {
            locationStatus.style.display = 'none';
            let key = 'coordinates.location_unavailable';
            switch (error.code) {
                case error.PERMISSION_DENIED: key = 'coordinates.location_denied'; break;
                case error.POSITION_UNAVAILABLE: key = 'coordinates.location_position_unavailable'; break;
                case error.TIMEOUT: key = 'coordinates.location_timeout'; break;
            }
            showToastForKey(key);
        }, { enableHighAccuracy: true, timeout: 10000, maximumAge: 0 });
    });
    coordForm.addEventListener('submit', async (event) => {
        event.preventDefault();
        const latitude = parseFloat(document.getElementById('lat').value);
        const longitude = parseFloat(document.getElementById('lon').value);
        const radius = document.getElementById('radius').value;
        const limit = document.getElementById('limit').value;
        loading.style.display = 'flex';
        results.style.display = 'none';
        emptyState.style.display = 'none';
        try {
            const url = `${citylistPath('/api/v1/cities/nearby')}?lat=${latitude}&lon=${longitude}&radius=${radius}&limit=${limit}`;
            const response = await fetch(url);
            const payload = await response.json();
            if (!response.ok || !payload.ok || !Array.isArray(payload.data)) throw new Error(payload.message || clientText('coordinates.location_request_failed'));
            loading.style.display = 'none';
            if (payload.data.length === 0) {
                emptyState.style.display = 'flex';
                return;
            }
            results.style.display = 'block';
            clientMessages = await getClientTranslations();
            resultCount.textContent = clientText('coordinates.nearby_result_count').replace('{count}', payload.data.length).replace('{radius}', radius);
            resultsList.innerHTML = payload.data.map((city, index) => `
                <div class="result-card ${index === 0 ? 'result-card-featured' : ''}">
                    <div class="result-card-header"><h3 class="result-card-title">${escapeHtml(city.name)}</h3><span class="result-card-badge">${escapeHtml(city.country)}</span></div>
                    <div class="result-card-body">
                        <div class="result-card-stat result-card-stat-highlight"><span class="result-card-stat-label">${escapeHtml(clientText('results.distance'))}</span><span class="result-card-stat-value">${city.distance.toFixed(2)} km</span></div>
                        <div class="result-card-stat"><span class="result-card-stat-label">${escapeHtml(clientText('results.latitude'))}</span><span class="result-card-stat-value">${city.coord.lat.toFixed(4)}</span></div>
                        <div class="result-card-stat"><span class="result-card-stat-label">${escapeHtml(clientText('results.longitude'))}</span><span class="result-card-stat-value">${city.coord.lon.toFixed(4)}</span></div>
                    </div>
                </div>`).join('');
        } catch (error) {
            loading.style.display = 'none';
            console.error('Search error:', error);
            clientMessages = await getClientTranslations();
            emptyState.querySelector('.empty-state-title').textContent = clientText('coordinates.search_error');
            emptyState.querySelector('.empty-state-description').textContent = clientText('coordinates.location_request_failed');
            emptyState.style.display = 'flex';
        }
    });
}

async function showUpdateNotification(registration) {
    if (document.querySelector('.update-banner')) return;
    clientMessages = await getClientTranslations();
    const banner = document.createElement('div');
    banner.className = 'update-banner';
    banner.setAttribute('role', 'status');
    const label = document.createElement('span');
    const formIsDirty = [...document.forms].some((form) => [...form.elements].some((field) => {
        if (field.disabled || !field.name) return false;
        if (field.type === 'checkbox' || field.type === 'radio') return field.checked !== field.defaultChecked;
        return field.value !== field.defaultValue;
    }));
    label.textContent = clientText(formIsDirty ? 'update_available_dirty' : 'update_available');
    const updateButton = document.createElement('button');
    updateButton.className = 'btn btn-primary';
    updateButton.textContent = clientText('update_now');
    const laterButton = document.createElement('button');
    laterButton.className = 'btn btn-secondary';
    laterButton.textContent = clientText('later');
    laterButton.addEventListener('click', () => banner.remove());
    updateButton.addEventListener('click', () => updateApp(registration));
    banner.append(label, updateButton, laterButton);
    document.body.appendChild(banner);
}

let deferredInstallPrompt;

function isInstalledPWA() {
    return window.matchMedia('(display-mode: standalone)').matches || window.navigator.standalone === true;
}

function hideInstallButton() {
    document.querySelector('.install-banner')?.remove();
}

async function showInstallButton(manual = false) {
    if (isInstalledPWA() || document.querySelector('.install-banner')) return;
    clientMessages = await getClientTranslations();
    const banner = document.createElement('div');
    banner.className = 'install-banner';
    banner.setAttribute('role', 'status');
    const label = document.createElement('span');
    label.textContent = manual ? clientText('install_instructions') : clientText('install_app');
    banner.append(label);
    if (!manual && deferredInstallPrompt) {
        const installButton = document.createElement('button');
        installButton.className = 'btn btn-primary';
        installButton.textContent = clientText('install_app');
        installButton.addEventListener('click', async () => {
            try {
                await deferredInstallPrompt.prompt();
                await deferredInstallPrompt.userChoice;
            } finally {
                deferredInstallPrompt = null;
                hideInstallButton();
            }
        });
        banner.append(installButton);
    }
    const closeButton = document.createElement('button');
    closeButton.className = 'btn btn-secondary';
    closeButton.textContent = clientText('dismiss');
    closeButton.addEventListener('click', hideInstallButton);
    banner.append(closeButton);
    document.body.appendChild(banner);
}

window.addEventListener('beforeinstallprompt', (event) => {
    event.preventDefault();
    deferredInstallPrompt = event;
    showInstallButton();
});

window.addEventListener('appinstalled', () => {
    hideInstallButton();
    deferredInstallPrompt = null;
});

let updateAccepted = false;
let reloading = false;

function reloadOnce() {
    if (reloading) return;
    reloading = true;
    window.location.reload();
}

function updateApp(registration) {
    updateAccepted = true;
    if (registration.waiting) {
        registration.waiting.postMessage({ type: 'SKIP_WAITING' });
        window.setTimeout(reloadOnce, 5000);
    } else {
        reloadOnce();
    }
}

function bindServiceWorkerUpdates(registration) {
    navigator.serviceWorker.addEventListener('controllerchange', () => {
        if (updateAccepted) reloadOnce();
    });
    if (registration.waiting && navigator.serviceWorker.controller) {
        showUpdateNotification(registration);
    }
    registration.addEventListener('updatefound', () => {
        const installing = registration.installing;
        if (!installing) return;
        installing.addEventListener('statechange', () => {
            if (installing.state === 'installed' && navigator.serviceWorker.controller) {
                showUpdateNotification(registration);
            }
        });
    });
    window.setInterval(() => registration.update(), 60 * 60 * 1000);
}

function bindNavigation() {
    const toggle = document.getElementById('nav-toggle');
    const panel = document.getElementById('nav-panel');
    const closeControl = panel?.querySelector('.nav-close');
    const header = document.querySelector('.header');
    const links = header?.querySelector('.nav-links');
    if (!toggle || !panel || !closeControl || !header || !links) return;

    panel.inert = !toggle.checked;
    toggle.addEventListener('change', () => {
        panel.inert = !toggle.checked;
        if (toggle.checked) {
            closeControl.focus();
        } else {
            toggle.focus();
        }
    });

    closeControl.addEventListener('keydown', (event) => {
        if (event.key !== 'Enter' && event.key !== ' ') return;
        event.preventDefault();
        toggle.checked = false;
        toggle.dispatchEvent(new Event('change', { bubbles: true }));
    });

    document.addEventListener('keydown', (event) => {
        if (!toggle.checked) return;
        if (event.key === 'Escape') {
            event.preventDefault();
            toggle.checked = false;
            toggle.dispatchEvent(new Event('change', { bubbles: true }));
            return;
        }
        if (event.key !== 'Tab' || !panel.contains(document.activeElement)) return;
        const focusable = [...panel.querySelectorAll('a[href], button:not([disabled]), [tabindex="0"]')]
            .filter((element) => !element.hidden && element.getAttribute('aria-hidden') !== 'true');
        if (focusable.length === 0) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (event.shiftKey && document.activeElement === first) {
            event.preventDefault();
            last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault();
            first.focus();
        }
    });

    const updateOverflow = () => {
        const collapsed = window.innerWidth >= 768 && links.scrollWidth > links.clientWidth + 1;
        document.documentElement.classList.toggle('nav-collapsed', collapsed);
    };
    updateOverflow();
    window.addEventListener('resize', updateOverflow, { passive: true });
}

document.addEventListener('click', async (event) => {
    const button = event.target.closest('[data-copy]');
    if (!button) return;
    const code = button.closest('.code-block')?.querySelector('pre')?.textContent;
    if (!code || !navigator.clipboard) return;
    await navigator.clipboard.writeText(code);
    clientMessages = await getClientTranslations();
    button.textContent = clientText('common.copied');
    window.setTimeout(() => { button.textContent = clientText('common.copy'); }, 2000);
});

document.addEventListener('DOMContentLoaded', () => {
    window.clearTimeout(bootWatchdog);
    document.querySelectorAll('.stat-value').forEach((element) => {
        const value = parseInt(element.textContent.replace(/,/g, ''), 10);
        if (!Number.isNaN(value)) element.textContent = formatNumber(value);
    });
    bindConsentControls();
    bindNavigation();
    bindSearchPage();
    bindCoordinatesPage();
    const isIOS = /iPad|iPhone|iPod/.test(window.navigator.userAgent) && !window.navigator.MSStream;
    if (isIOS && !isInstalledPWA()) showInstallButton(true);
    if ('serviceWorker' in navigator) {
        window.addEventListener('load', () => {
            navigator.serviceWorker.register(citylistPath('/sw.js'))
                .then(bindServiceWorkerUpdates)
                .catch((error) => console.log('SW registration failed:', error));
        });
    }
});
