using System;

namespace TallyWhatsappsender.Config
{
    public enum LogLevel
    {
        Debug,
        Info,
        Warning,
        Error
    }

    public class LoggingSettings
    {
        public string LogFilePath { get; set; }
        public LogLevel LogLevel { get; set; }
        public bool EnableConsoleLogging { get; set; }
        public bool EnableFileLogging { get; set; }

        public LoggingSettings()
        {
            SetDefaults();
        }

        private void SetDefaults()
        {
            LogFilePath = "WhatsAppSender.log";
            LogLevel = LogLevel.Info;
            EnableConsoleLogging = true;
            EnableFileLogging = true;
        }

        public void Validate()
        {
            if (string.IsNullOrEmpty(LogFilePath))
            {
                throw new InvalidOperationException("LogFilePath cannot be empty");
            }
        }

        public override string ToString()
        {
            return string.Format(
                "LoggingSettings: LogFilePath={0}, LogLevel={1}, EnableConsoleLogging={2}, EnableFileLogging={3}",
                LogFilePath, LogLevel, EnableConsoleLogging, EnableFileLogging
            );
        }
    }
}
