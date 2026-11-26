(() => {
    const mount = document.getElementById('swagger-ui');
    if (!mount || !window.SwaggerUIBundle || !window.SwaggerUIStandalonePreset) return;

    window.SwaggerUIBundle({
        url: mount.dataset.openapiUrl,
        dom_id: '#swagger-ui',
        deepLinking: true,
        validatorUrl: null,
        persistAuthorization: false,
        presets: [window.SwaggerUIBundle.presets.apis, window.SwaggerUIStandalonePreset],
        plugins: [window.SwaggerUIBundle.plugins.DownloadUrl],
        layout: 'BaseLayout',
        docExpansion: 'list',
    });
})();
