using System;
using System.Collections.Generic;
using TallyWhatsappsender.Config;

namespace TallyWhatsappsender
{
    /// <summary>
    /// Static wrapper so that TallyInterface.cs can simply call
    ///   MessageTemplates.GetTemplate("Invoice")
    /// without needing to instantiate anything.
    /// Delegates to ConfigManager's loaded templates behind the scenes.
    /// </summary>
    public static class MessageTemplates
    {
        /// <summary>
        /// Look up a template by name.  Returns null if not found.
        /// </summary>
        public static Template GetTemplate(string name)
        {
            try
            {
                var mt = ConfigManager.Instance.MessageTemplates;
                var inner = mt.GetTemplate(name);
                if (inner == null) return null;

                return new Template(inner);
            }
            catch
            {
                return null;
            }
        }

        /// <summary>
        /// Thin adapter that exposes the ApplyData() method TallyInterface.cs relies on.
        /// templateData is a semicolon-separated list of key=value pairs, e.g.
        ///   "CustomerName=John;InvoiceNumber=INV001;Amount=1500"
        /// </summary>
        public class Template
        {
            private readonly MessageTemplate _inner;

            internal Template(MessageTemplate inner)
            {
                _inner = inner;
            }

            public string ApplyData(string templateData)
            {
                var values = ParseKeyValues(templateData);
                return _inner.RenderTemplate(values);
            }

            private static Dictionary<string, string> ParseKeyValues(string data)
            {
                var dict = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
                if (string.IsNullOrEmpty(data)) return dict;

                foreach (string pair in data.Split(';'))
                {
                    int idx = pair.IndexOf('=');
                    if (idx > 0)
                    {
                        string key   = pair.Substring(0, idx).Trim();
                        string value = pair.Substring(idx + 1).Trim();
                        dict[key] = value;
                    }
                }
                return dict;
            }
        }
    }
}
