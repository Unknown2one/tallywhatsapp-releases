using System;

namespace TallyWhatsappsender.Config
{
    public class WhatsAppSettings
    {
        public string BridgeUrl { get; set; }
        public int ApiTimeout { get; set; }
        public int MaxRetries { get; set; }
        public int RetryDelay { get; set; }
        public int SeleniumTimeout { get; set; }
        public int ElementWaitTimeout { get; set; }
        public string ChromeDriverPath { get; set; }
        public int MessageDelay { get; set; }
        public int ContactValidationLength { get; set; }

        public WhatsAppSettings()
        {
            SetDefaults();
        }

        private void SetDefaults()
        {
            BridgeUrl = "http://localhost:8080";
            ApiTimeout = 30;
            MaxRetries = 3;
            RetryDelay = 1000;
            SeleniumTimeout = 60;
            ElementWaitTimeout = 30;
            ChromeDriverPath = "chromedriver.exe";
            MessageDelay = 3000;
            ContactValidationLength = 12;
        }

        public void Validate()
        {
            if (string.IsNullOrEmpty(BridgeUrl))
            {
                throw new InvalidOperationException("WhatsAppBridgeUrl cannot be empty");
            }

            if (ApiTimeout <= 0)
            {
                throw new InvalidOperationException("ApiTimeout must be greater than 0");
            }

            if (MaxRetries < 0)
            {
                throw new InvalidOperationException("MaxRetries cannot be negative");
            }

            if (RetryDelay < 0)
            {
                throw new InvalidOperationException("RetryDelay cannot be negative");
            }

            if (SeleniumTimeout <= 0)
            {
                throw new InvalidOperationException("SeleniumTimeout must be greater than 0");
            }

            if (ElementWaitTimeout <= 0)
            {
                throw new InvalidOperationException("ElementWaitTimeout must be greater than 0");
            }

            if (string.IsNullOrEmpty(ChromeDriverPath))
            {
                throw new InvalidOperationException("ChromeDriverPath cannot be empty");
            }

            if (MessageDelay < 0)
            {
                throw new InvalidOperationException("MessageDelay cannot be negative");
            }

            if (ContactValidationLength <= 0)
            {
                throw new InvalidOperationException("ContactValidationLength must be greater than 0");
            }
        }

        public override string ToString()
        {
            return string.Format(
                "WhatsAppSettings: BridgeUrl={0}, ApiTimeout={1}, MaxRetries={2}, RetryDelay={3}, " +
                "SeleniumTimeout={4}, ElementWaitTimeout={5}, ChromeDriverPath={6}, MessageDelay={7}, ContactValidationLength={8}",
                BridgeUrl, ApiTimeout, MaxRetries, RetryDelay, SeleniumTimeout, ElementWaitTimeout, 
                ChromeDriverPath, MessageDelay, ContactValidationLength
            );
        }
    }
}
