(function () {
  // Vue app for Subscription page
  const el = document.getElementById('subscription-data');
  if (!el) return;
  const textarea = document.getElementById('subscription-links');
  const rawLinks = (textarea?.value || '').split('\n').filter(Boolean);

  // Try to read encrypted URLs from JSON script tag first
  let encryptedUrls = { happEncryptedUrl: null, v2raytunEncryptedUrl: null };
  try {
    const jsonScript = document.getElementById('subscription-encrypted-urls');
    if (jsonScript && jsonScript.textContent) {
      let jsonText = jsonScript.textContent.trim();
      console.info('[Subscription] JSON script tag content (raw):', jsonText);
      
      // Remove surrounding quotes if JSON is escaped as a string
      if (jsonText.startsWith('"') && jsonText.endsWith('"')) {
        // JSON is escaped as a string, need to unescape it
        try {
          jsonText = JSON.parse(jsonText); // Parse the escaped string to get the actual JSON string
          console.info('[Subscription] Unescaped JSON string:', jsonText);
        } catch (e) {
          console.warn('[Subscription] Failed to unescape JSON string:', e);
        }
      }
      
      // Now parse the actual JSON
      encryptedUrls = JSON.parse(jsonText);
      console.info('[Subscription] Parsed encrypted URLs:', encryptedUrls);
      console.info('[Subscription] happEncryptedUrl:', encryptedUrls.happEncryptedUrl);
      console.info('[Subscription] v2raytunEncryptedUrl:', encryptedUrls.v2raytunEncryptedUrl);
    } else {
      console.warn('[Subscription] JSON script tag not found or empty');
    }
  } catch (e) {
    console.warn('[Subscription] Failed to parse encrypted URLs from JSON:', e);
  }

  const data = {
    sId: el.getAttribute('data-sid') || '',
    subUrl: el.getAttribute('data-sub-url') || '',
    subJsonUrl: el.getAttribute('data-subjson-url') || '',
    download: el.getAttribute('data-download') || '',
    upload: el.getAttribute('data-upload') || '',
    used: el.getAttribute('data-used') || '',
    total: el.getAttribute('data-total') || '',
    remained: el.getAttribute('data-remained') || '',
    expireMs: (parseInt(el.getAttribute('data-expire') || '0', 10) || 0) * 1000,
    lastOnlineMs: (parseInt(el.getAttribute('data-lastonline') || '0', 10) || 0),
    downloadByte: parseInt(el.getAttribute('data-downloadbyte') || '0', 10) || 0,
    uploadByte: parseInt(el.getAttribute('data-uploadbyte') || '0', 10) || 0,
    totalByte: parseInt(el.getAttribute('data-totalbyte') || '0', 10) || 0,
    datepicker: el.getAttribute('data-datepicker') || 'gregorian',
    hideConfigLinks: el.getAttribute('data-hideconfiglinks') === 'true',
    showOnlyHappV2RayTun: el.getAttribute('data-showonlyhappv2raytun') === 'true',
    happEncryptedUrl: (() => {
      // Prefer JSON value, but check if it's not null/empty
      if (encryptedUrls.happEncryptedUrl && encryptedUrls.happEncryptedUrl.trim() !== '') {
        return encryptedUrls.happEncryptedUrl;
      }
      // Fallback to data attribute
      const val = el.getAttribute('data-happ-encrypted-url') || '';
      return (val && val !== '#ZgotmplZ' && val.trim() !== '') ? val : '';
    })(),
    v2raytunEncryptedUrl: (() => {
      // Prefer JSON value, but check if it's not null/empty
      if (encryptedUrls.v2raytunEncryptedUrl && encryptedUrls.v2raytunEncryptedUrl.trim() !== '') {
        return encryptedUrls.v2raytunEncryptedUrl;
      }
      // Fallback to data attribute
      const val = el.getAttribute('data-v2raytun-encrypted-url') || '';
      return (val && val !== '#ZgotmplZ' && val.trim() !== '') ? val : '';
    })(),
    theme: el.getAttribute('data-theme') || '',
    logoUrl: el.getAttribute('data-logo-url') || '',
    brandText: el.getAttribute('data-brand-text') || '',
  };

  // Normalize lastOnline to milliseconds if it looks like seconds
  if (data.lastOnlineMs && data.lastOnlineMs < 10_000_000_000) {
    data.lastOnlineMs *= 1000;
  }

  function renderLink(item) {
    return (
      Vue.h('a-list-item', {}, [
        Vue.h('a-space', { props: { size: 'small' } }, [
          Vue.h('a-button', { props: { size: 'small' }, on: { click: () => copy(item) } }, [Vue.h('a-icon', { props: { type: 'copy' } })]),
          Vue.h('span', { class: 'break-all' }, item)
        ])
      ])
    );
  }

  function copy(text) {
    console.info('[Subscription] Copying to clipboard:', text);
    ClipboardManager.copyText(text).then(ok => {
      const messageType = ok ? 'success' : 'error';
      Vue.prototype.$message[messageType](ok ? 'Copied' : 'Copy failed');
    });
  }

  function open(url) {
    console.info('[Subscription] Opening URL:', url);
    window.location.href = url;
  }

  function drawQR(value, elementId = 'qrcode') {
    try {
      const element = document.getElementById(elementId);
      if (element) {
        new QRious({ element: element, value, size: 220 });
      }
    } catch (e) {
      console.warn(e);
    }
  }

  // Try to extract a human label (email/ps) from different link types
  function linkName(link, idx) {
    try {
      if (link.startsWith('vmess://')) {
        const json = JSON.parse(atob(link.replace('vmess://', '')));
        if (json.ps) return json.ps;
        if (json.add && json.id) return json.add; // fallback host
      } else if (link.startsWith('vless://') || link.startsWith('trojan://')) {
        const hashIdx = link.indexOf('#');
        if (hashIdx !== -1) return decodeURIComponent(link.substring(hashIdx + 1));
        const qIdx = link.indexOf('?');
        if (qIdx !== -1) {
          const qs = new URL('http://x/?' + link.substring(qIdx + 1, hashIdx !== -1 ? hashIdx : undefined)).searchParams;
          if (qs.get('remark')) return qs.get('remark');
          if (qs.get('email')) return qs.get('email');
        }
        const at = link.indexOf('@');
        const protSep = link.indexOf('://');
        if (at !== -1 && protSep !== -1) return link.substring(protSep + 3, at);
      } else if (link.startsWith('ss://')) {
        const hashIdx = link.indexOf('#');
        if (hashIdx !== -1) return decodeURIComponent(link.substring(hashIdx + 1));
      }
    } catch (e) { /* ignore and fallback */ }
    return 'Link ' + (idx + 1);
  }

  const app = new Vue({
    delimiters: ['[[', ']]'],
    el: '#app',
    data: {
      themeSwitcher,
      app: data,
      links: rawLinks,
      lang: '',
      viewportWidth: (typeof window !== 'undefined' ? window.innerWidth : 1024),
      hideConfigLinks: data.hideConfigLinks,
      showOnlyHappV2RayTun: data.showOnlyHappV2RayTun || false,
      theme: data.theme || '',
      logoUrl: data.logoUrl || '',
      brandText: data.brandText || '',
    },
    async mounted() {
      this.lang = LanguageManager.getLanguage();
      const tpl = document.getElementById('subscription-data');
      const sj = tpl ? tpl.getAttribute('data-subjson-url') : '';
      if (sj) this.app.subJsonUrl = sj;
      
      // Apply theme class to root element if theme is set
      if (this.theme) {
        const rootEl = document.getElementById('app');
        if (rootEl) {
          rootEl.classList.add('subscription-theme-' + this.theme);
        }
      }
      
      // Log subscription URLs
      console.info('[Subscription] Loaded subscription data:');
      console.info('  - Plain subscription URL:', this.app.subUrl);
      console.info('  - Happ encrypted URL:', this.app.happEncryptedUrl || '(not available)');
      console.info('  - V2RayTun encrypted URL:', this.app.v2raytunEncryptedUrl || '(not available)');
      console.info('  - hideConfigLinks:', this.hideConfigLinks, '(type:', typeof this.hideConfigLinks + ')');
      console.info('  - showDualEncryptedQR:', this.showDualEncryptedQR);
      console.info('  - Theme:', this.theme || '(default)');
      console.info('  - Logo URL:', this.logoUrl || '(not set)');
      console.info('  - Brand Text:', this.brandText || '(not set)');
      
      // Draw QR codes based on mode
      if (this.showDualEncryptedQR) {
        // Draw dual QR codes for Happ and V2RayTun
        drawQR(this.app.happEncryptedUrl, 'qrcode-happ');
        drawQR(this.app.v2raytunEncryptedUrl, 'qrcode-v2raytun');
        console.info('[Subscription] Drawing dual encrypted QR codes for Happ and V2RayTun');
      } else {
        // Draw default QR code(s)
      drawQR(this.app.subUrl);
      try {
        const elJson = document.getElementById('qrcode-subjson');
        if (elJson && this.app.subJsonUrl) {
          new QRious({ element: elJson, value: this.app.subJsonUrl, size: 220 });
        }
      } catch (e) { /* ignore */ }
      }
      this._onResize = () => { this.viewportWidth = window.innerWidth; };
      window.addEventListener('resize', this._onResize);
    },
    beforeDestroy() {
      if (this._onResize) window.removeEventListener('resize', this._onResize);
    },
    computed: {
      isMobile() {
        return this.viewportWidth < 576;
      },
      isUnlimited() {
        return !this.app.totalByte;
      },
      isActive() {
        const now = Date.now();
        const expiryOk = !this.app.expireMs || this.app.expireMs >= now;
        const trafficOk = !this.app.totalByte || (this.app.uploadByte + this.app.downloadByte) <= this.app.totalByte;
        return expiryOk && trafficOk;
      },
      showDualEncryptedQR() {
        // Show dual QR codes when encryption is enabled and showOnlyHappV2RayTun is true
        return this.showOnlyHappV2RayTun && 
               this.app.happEncryptedUrl && 
               this.app.happEncryptedUrl.trim() !== '' &&
               this.app.v2raytunEncryptedUrl && 
               this.app.v2raytunEncryptedUrl.trim() !== '';
      },
      shadowrocketUrl() {
        const rawUrl = this.app.subUrl + '?flag=shadowrocket';
        const base64Url = btoa(rawUrl);
        const remark = encodeURIComponent(this.app.sId || 'Subscription');
        return `shadowrocket://add/sub/${base64Url}?remark=${remark}`;
      },
      v2boxUrl() {
        return `v2box://install-sub?url=${encodeURIComponent(this.app.subUrl)}&name=${encodeURIComponent(this.app.sId)}`;
      },
      streisandUrl() {
        return `streisand://import/${encodeURIComponent(this.app.subUrl)}`;
      },
      v2raytunUrl() {
        // Use encrypted URL if available, otherwise use plain subscription URL
        if (this.app.v2raytunEncryptedUrl) {
          console.info('[Subscription] V2RayTun: Using encrypted URL:', this.app.v2raytunEncryptedUrl);
          return this.app.v2raytunEncryptedUrl;
        }
        console.info('[Subscription] V2RayTun: Using plain subscription URL:', this.app.subUrl);
        return this.app.subUrl;
      },
      npvtunUrl() {
        return this.app.subUrl;
      },
      happUrl() {
        // Use encrypted URL if available, otherwise use plain subscription URL
        if (this.app.happEncryptedUrl) {
          console.info('[Subscription] Happ: Using encrypted URL:', this.app.happEncryptedUrl);
          return this.app.happEncryptedUrl;
        }
        const plainUrl = `happ://add/${encodeURIComponent(this.app.subUrl)}`;
        console.info('[Subscription] Happ: Using plain URL:', plainUrl);
        return plainUrl;
      }
    },
    methods: {
      renderLink,
      copy,
      open,
      linkName,
      i18nLabel(key) {
        return '{{ i18n "' + key + '" }}';
      },
    },
  });
})();
