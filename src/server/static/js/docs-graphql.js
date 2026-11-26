(() => {
    const mount = document.getElementById('graphiql');
    if (!mount || !window.React || !window.ReactDOM?.createRoot || !window.GraphiQL?.GraphiQL) return;

    const endpoint = mount.dataset.endpoint;
    const fetcher = async (params) => {
        const response = await fetch(endpoint, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(params),
        });
        const result = await response.json();
        if (!response.ok && !result.errors) throw new Error(`GraphQL request failed with status ${response.status}`);
        return result;
    };
    const defaultQuery = `query {
  cities(search: "Tokyo", limit: 5) {
    id
    name
    country
    coord { lat lon }
  }
}`;
    const colorScheme = window.matchMedia('(prefers-color-scheme: dark)');
    const syncTheme = () => {
        const root = document.documentElement;
        const theme = root.classList.contains('theme-auto')
            ? (colorScheme.matches ? 'dark' : 'light')
            : (root.classList.contains('theme-light') ? 'light' : 'dark');
        document.body.classList.toggle('graphiql-dark', theme === 'dark');
        document.body.classList.toggle('graphiql-light', theme === 'light');
    };

    syncTheme();
    colorScheme.addEventListener('change', syncTheme);
    new MutationObserver(syncTheme).observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    const root = window.ReactDOM.createRoot(mount);
    root.render(window.React.createElement(window.GraphiQL.GraphiQL, { fetcher, defaultQuery }));
})();
