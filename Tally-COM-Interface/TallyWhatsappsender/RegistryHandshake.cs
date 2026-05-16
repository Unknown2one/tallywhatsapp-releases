using System;
using Microsoft.Win32;

namespace TallyWhatsappsender
{
    /// <summary>
    /// Reads the port + HMAC secret published by the Go service at startup.
    ///
    /// The service writes:
    ///   HKLM\SOFTWARE\TallyWhatsApp\Port    (DWORD, dynamic free port)
    ///   HKLM\SOFTWARE\TallyWhatsApp\Secret  (REG_SZ, hex-encoded 32 bytes)
    ///   HKLM\SOFTWARE\TallyWhatsApp\Version (REG_SZ, informational)
    ///
    /// We MUST use the 64-bit view: the DLL is loaded by 64-bit Tally,
    /// but COM activation can also produce 32-bit views which point at
    /// the WoW6432Node mirror. RegistryView.Registry64 keeps both
    /// processes pointing at the same key.
    /// </summary>
    public static class RegistryHandshake
    {
        private const string SubKeyPath = @"SOFTWARE\TallyWhatsApp";

        public class HandshakeData
        {
            public int Port { get; set; }
            public byte[] Secret { get; set; }
        }

        /// <summary>
        /// Reads the port + secret. Throws if either is missing — the
        /// service must be running for the DLL to do anything useful.
        /// </summary>
        public static HandshakeData Load()
        {
            using (var hklm = RegistryKey.OpenBaseKey(RegistryHive.LocalMachine, RegistryView.Registry64))
            using (var key = hklm.OpenSubKey(SubKeyPath))
            {
                if (key == null)
                {
                    throw new InvalidOperationException(
                        "TallyWhatsApp service is not installed (HKLM\\" + SubKeyPath + " missing). " +
                        "Reinstall TallyWhatsApp from the official installer.");
                }

                object portObj = key.GetValue("Port");
                object secretObj = key.GetValue("Secret");
                if (portObj == null || secretObj == null)
                {
                    throw new InvalidOperationException(
                        "TallyWhatsApp service has not started yet. " +
                        "Start the TallyWhatsAppConnector service and try again.");
                }

                int port = Convert.ToInt32(portObj);
                if (port <= 0 || port > 65535)
                {
                    throw new InvalidOperationException(
                        "TallyWhatsApp service published an invalid port (" + port + ").");
                }

                string hex = secretObj.ToString();
                byte[] secret = HexDecode(hex);
                if (secret == null || secret.Length != 32)
                {
                    throw new InvalidOperationException(
                        "TallyWhatsApp HMAC secret in registry is malformed.");
                }

                return new HandshakeData { Port = port, Secret = secret };
            }
        }

        /// <summary>
        /// Tries to load handshake data, returning null on any failure.
        /// Used by health checks where we want to report "not installed"
        /// rather than throw to Tally.
        /// </summary>
        public static HandshakeData TryLoad()
        {
            try { return Load(); }
            catch { return null; }
        }

        private static byte[] HexDecode(string hex)
        {
            if (string.IsNullOrEmpty(hex) || (hex.Length % 2) != 0)
                return null;
            byte[] result = new byte[hex.Length / 2];
            for (int i = 0; i < result.Length; i++)
            {
                int hi = HexNibble(hex[i * 2]);
                int lo = HexNibble(hex[i * 2 + 1]);
                if (hi < 0 || lo < 0) return null;
                result[i] = (byte)((hi << 4) | lo);
            }
            return result;
        }

        private static int HexNibble(char c)
        {
            if (c >= '0' && c <= '9') return c - '0';
            if (c >= 'a' && c <= 'f') return 10 + (c - 'a');
            if (c >= 'A' && c <= 'F') return 10 + (c - 'A');
            return -1;
        }
    }
}
