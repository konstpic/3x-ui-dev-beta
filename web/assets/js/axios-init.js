axios.defaults.headers.post['Content-Type'] = 'application/x-www-form-urlencoded; charset=UTF-8';
axios.defaults.headers.common['X-Requested-With'] = 'XMLHttpRequest';

axios.interceptors.request.use(
    (config) => {
        if (config.data instanceof FormData) {
            config.headers['Content-Type'] = 'multipart/form-data';
        } else if (config.headers && config.headers['Content-Type'] === 'application/json') {
            // If Content-Type is explicitly set to JSON, send as JSON (don't convert to form-urlencoded)
            // This allows empty arrays to be properly sent
            config.data = JSON.stringify(config.data);
        } else {
            config.data = Qs.stringify(config.data, {
                arrayFormat: 'repeat',
            });
        }
        return config;
    },
    (error) => Promise.reject(error),
);

axios.interceptors.response.use(
    (response) => response,
    (error) => {
        if (error.response) {
            const statusCode = error.response.status;
            // Check the status code
            if (statusCode === 401) { // Unauthorized
                return window.location.reload();
            }
        }
        return Promise.reject(error);
    }
);
