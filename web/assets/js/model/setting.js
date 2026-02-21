class AllSetting {

    constructor(data) {
        this.webListen = "";
        this.webDomain = "";
        this.webPort = 2053;
        this.webCertFile = "";
        this.webKeyFile = "";
        this.webBasePath = "/";
        this.sessionMaxAge = 360;
        this.pageSize = 25;
        this.expireDiff = 0;
        this.trafficDiff = 0;
        this.remarkModel = "-ieo";
        this.datepicker = "gregorian";
        this.tgBotEnable = false;
        this.tgBotToken = "";
        this.tgBotProxy = "";
        this.tgBotAPIServer = "";
        this.tgBotChatId = "";
        this.tgRunTime = "@daily";
        this.tgBotBackup = false;
        this.tgBotLoginNotify = true;
        this.tgCpu = 80;
        this.tgLang = "en-US";
        this.twoFactorEnable = false;
        this.twoFactorToken = "";
        this.xrayTemplateConfig = "";
        this.subEnable = true;
        this.subJsonEnable = false;
        this.subTitle = "";
        this.subListen = "";
        this.subPort = 2096;
        this.subPath = "/sub/";
        this.subJsonPath = "/json/";
        this.subDomain = "";
        this.externalTrafficInformEnable = false;
        this.externalTrafficInformURI = "";
        this.subCertFile = "";
        this.subKeyFile = "";
        this.subUpdates = 12;
        this.subEncrypt = true;
        this.subShowInfo = true;
        this.subURI = "";
        this.subJsonURI = "";
        this.subJsonFragment = "";
        this.subJsonNoises = "";
        this.subJsonMux = "";
        this.subJsonRules = "";
        this.subEncryptHappV2RayTun = false;
        this.subOnlyHappV2RayTun = false;
        this.subHideConfigLinks = false;
        this.subShowOnlyHappV2RayTun = false;
        this.subAutoRotateKeys = false; // Automatically rotate client keys before subscription update interval
        this.subHeaders = "{}"; // JSON string for subscription headers
        this.subProviderID = ""; // Provider ID for Happ extended headers
        this.subProviderIDMethod = "url"; // Method to send Provider ID: "url", "header", "none"
        this.subPageTheme = ""; // Subscription page theme: "rainbow", "coffee", "banana", "sunset"
        this.subPageLogoUrl = ""; // Logo URL for subscription page
        this.subPageBrandText = ""; // Brand text for subscription page
        this.subPageBackgroundUrl = ""; // Background image URL for subscription card

        this.timeLocation = "Local";

        // LDAP settings
        this.ldapEnable = false;
        this.ldapHost = "";
        this.ldapPort = 389;
        this.ldapUseTLS = false;
        this.ldapBindDN = "";
        this.ldapPassword = "";
        this.ldapBaseDN = "";
        this.ldapUserFilter = "(objectClass=person)";
        this.ldapUserAttr = "mail";
        this.ldapVlessField = "vless_enabled";
        this.ldapSyncCron = "@every 1m";
        this.ldapFlagField = "";
        this.ldapTruthyValues = "true,1,yes,on";
        this.ldapInvertFlag = false;
        this.ldapInboundTags = "";
        this.ldapAutoCreate = false;
        this.ldapAutoDelete = false;
        this.ldapDefaultTotalGB = 0;
        this.ldapDefaultExpiryDays = 0;
        this.ldapDefaultLimitIP = 0;

        // Multi-node mode settings
        this.multiNodeMode = false; // Multi-node mode setting
        
        // HWID tracking mode
        // "off" = HWID tracking disabled
        // "client_header" = HWID provided by client via x-hwid header (default, recommended)
        // "legacy_fingerprint" = deprecated fingerprint-based HWID generation (deprecated, for backward compatibility only)
        this.hwidMode = "client_header"; // HWID tracking mode
        
        // Grafana integration settings
        this.grafanaLokiUrl = ""; // Loki API URL
        this.grafanaVictoriaMetricsUrl = ""; // VictoriaMetrics API URL
        this.grafanaEnable = false; // Enable Grafana integration

        if (data == null) {
            return
        }
        
        ObjectUtil.cloneProps(this, data);
        
        // Ensure new subscription page customization fields are strings (handle undefined/null)
        if (this.subPageTheme === undefined || this.subPageTheme === null) {
            this.subPageTheme = "";
        }
        if (this.subPageLogoUrl === undefined || this.subPageLogoUrl === null) {
            this.subPageLogoUrl = "";
        }
        if (this.subPageBrandText === undefined || this.subPageBrandText === null) {
            this.subPageBrandText = "";
        }
        // Ensure subProviderIDMethod is set (default to "url" for backward compatibility)
        if (this.subProviderIDMethod === undefined || this.subProviderIDMethod === null || this.subProviderIDMethod === "") {
            this.subProviderIDMethod = "url";
        }
        if (this.subPageBackgroundUrl === undefined || this.subPageBackgroundUrl === null) {
            this.subPageBackgroundUrl = "";
        }
        
        // Ensure multiNodeMode is boolean (handle string "true"/"false" from backend)
        if (this.multiNodeMode !== undefined && this.multiNodeMode !== null) {
            if (typeof this.multiNodeMode === 'string') {
                this.multiNodeMode = this.multiNodeMode === 'true' || this.multiNodeMode === '1';
            } else {
                this.multiNodeMode = Boolean(this.multiNodeMode);
            }
        } else {
            this.multiNodeMode = false;
        }
        
        // Ensure boolean fields are set from data (same pattern as multiNodeMode)
        if (data.subEncryptHappV2RayTun !== undefined && data.subEncryptHappV2RayTun !== null) {
            this.subEncryptHappV2RayTun = Boolean(data.subEncryptHappV2RayTun);
        }
        if (data.subOnlyHappV2RayTun !== undefined && data.subOnlyHappV2RayTun !== null) {
            this.subOnlyHappV2RayTun = Boolean(data.subOnlyHappV2RayTun);
        }
        if (data.subHideConfigLinks !== undefined && data.subHideConfigLinks !== null) {
            this.subHideConfigLinks = Boolean(data.subHideConfigLinks);
        }
        if (data.subShowOnlyHappV2RayTun !== undefined && data.subShowOnlyHappV2RayTun !== null) {
            this.subShowOnlyHappV2RayTun = Boolean(data.subShowOnlyHappV2RayTun);
        }
        
        // Ensure hwidMode is valid string (default to "client_header" if invalid)
        if (this.hwidMode === undefined || this.hwidMode === null) {
            this.hwidMode = "client_header";
        } else if (typeof this.hwidMode !== 'string') {
            this.hwidMode = String(this.hwidMode);
        }
        // Validate hwidMode value
        const validHwidModes = ["off", "client_header", "legacy_fingerprint"];
        if (!validHwidModes.includes(this.hwidMode)) {
            this.hwidMode = "client_header"; // Default to client_header if invalid
        }
    }

    equals(other) {
        return ObjectUtil.equals(this, other);
    }
}