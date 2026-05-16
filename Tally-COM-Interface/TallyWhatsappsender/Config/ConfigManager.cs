using System;
using System.Configuration;
using System.IO;

namespace TallyWhatsappsender.Config
{
    public class ConfigManager
    {
        private static ConfigManager instance;
        private static readonly object lockObject = new object();

        public WhatsAppSettings WhatsAppSettings { get; private set; }
        public LoggingSettings LoggingSettings { get; private set; }
        public MessageTemplates MessageTemplates { get; private set; }

        private ConfigManager()
        {
            LoadConfiguration();
        }

        public static ConfigManager Instance
        {
            get
            {
                if (instance == null)
                {
                    lock (lockObject)
                    {
                        if (instance == null)
                        {
                            instance = new ConfigManager();
                        }
                    }
                }
                return instance;
            }
        }

        private void LoadConfiguration()
        {
            WhatsAppSettings = LoadWhatsAppSettings();
            LoggingSettings = LoadLoggingSettings();
            MessageTemplates = LoadMessageTemplates();

            ValidateConfiguration();
        }

        private WhatsAppSettings LoadWhatsAppSettings()
        {
            var settings = new WhatsAppSettings();

            settings.BridgeUrl = GetConfigValue("WhatsAppBridgeUrl", settings.BridgeUrl);
            settings.ApiTimeout = GetConfigValueAsInt("ApiTimeout", settings.ApiTimeout);
            settings.MaxRetries = GetConfigValueAsInt("MaxRetries", settings.MaxRetries);
            settings.RetryDelay = GetConfigValueAsInt("RetryDelay", settings.RetryDelay);
            settings.SeleniumTimeout = GetConfigValueAsInt("SeleniumTimeout", settings.SeleniumTimeout);
            settings.ElementWaitTimeout = GetConfigValueAsInt("ElementWaitTimeout", settings.ElementWaitTimeout);
            settings.ChromeDriverPath = GetConfigValue("ChromeDriverPath", settings.ChromeDriverPath);
            settings.MessageDelay = GetConfigValueAsInt("MessageDelay", settings.MessageDelay);
            settings.ContactValidationLength = GetConfigValueAsInt("ContactValidationLength", settings.ContactValidationLength);

            return settings;
        }

        private LoggingSettings LoadLoggingSettings()
        {
            var settings = new LoggingSettings();

            settings.LogFilePath = GetConfigValue("LogFilePath", settings.LogFilePath);
            settings.LogLevel = ParseLogLevel(GetConfigValue("LogLevel", settings.LogLevel.ToString()));
            settings.EnableConsoleLogging = GetConfigValueAsBool("EnableConsoleLogging", settings.EnableConsoleLogging);
            settings.EnableFileLogging = GetConfigValueAsBool("EnableFileLogging", settings.EnableFileLogging);

            return settings;
        }

        private MessageTemplates LoadMessageTemplates()
        {
            string templatesPath = GetConfigValue("MessageTemplatesPath", "Templates\\MessageTemplates.xml");
            
            string fullPath = templatesPath;
            if (!Path.IsPathRooted(templatesPath))
            {
                fullPath = Path.Combine(AppDomain.CurrentDomain.BaseDirectory, templatesPath);
            }

            if (File.Exists(fullPath))
            {
                return new MessageTemplates(fullPath);
            }
            else
            {
                return new MessageTemplates();
            }
        }

        private void ValidateConfiguration()
        {
            try
            {
                WhatsAppSettings.Validate();
                LoggingSettings.Validate();
            }
            catch (Exception ex)
            {
                throw new ConfigurationErrorsException("Configuration validation failed: " + ex.Message, ex);
            }
        }

        public void ReloadConfiguration()
        {
            lock (lockObject)
            {
                ConfigurationManager.RefreshSection("appSettings");
                LoadConfiguration();
            }
        }

        private string GetConfigValue(string key, string defaultValue)
        {
            try
            {
                string value = ConfigurationManager.AppSettings[key];
                return string.IsNullOrEmpty(value) ? defaultValue : value;
            }
            catch
            {
                return defaultValue;
            }
        }

        private int GetConfigValueAsInt(string key, int defaultValue)
        {
            try
            {
                string value = ConfigurationManager.AppSettings[key];
                int result;
                if (int.TryParse(value, out result))
                {
                    return result;
                }
                return defaultValue;
            }
            catch
            {
                return defaultValue;
            }
        }

        private bool GetConfigValueAsBool(string key, bool defaultValue)
        {
            try
            {
                string value = ConfigurationManager.AppSettings[key];
                bool result;
                if (bool.TryParse(value, out result))
                {
                    return result;
                }
                return defaultValue;
            }
            catch
            {
                return defaultValue;
            }
        }

        private LogLevel ParseLogLevel(string value)
        {
            try
            {
                return (LogLevel)Enum.Parse(typeof(LogLevel), value, true);
            }
            catch
            {
                return LogLevel.Info;
            }
        }

        public string GetDiagnosticInfo()
        {
            return string.Format(
                "Configuration Diagnostics:\n" +
                "-------------------------\n" +
                "{0}\n\n{1}\n",
                WhatsAppSettings.ToString(),
                LoggingSettings.ToString()
            );
        }
    }
}
