using System;
using System.Collections.Generic;
using System.IO;
using System.Xml;

namespace TallyWhatsappsender.Config
{
    public class MessageTemplate
    {
        public string Name { get; set; }
        public string Content { get; set; }
        public Dictionary<string, string> Placeholders { get; set; }

        public MessageTemplate()
        {
            Placeholders = new Dictionary<string, string>();
        }

        public string RenderTemplate(Dictionary<string, string> values)
        {
            string result = Content;
            
            if (values != null)
            {
                foreach (var kvp in values)
                {
                    string placeholder = "{" + kvp.Key + "}";
                    result = result.Replace(placeholder, kvp.Value);
                }
            }

            return result;
        }
    }

    public class MessageTemplates
    {
        private Dictionary<string, MessageTemplate> templates;
        private string templatesFilePath;

        public MessageTemplates(string filePath)
        {
            templatesFilePath = filePath;
            templates = new Dictionary<string, MessageTemplate>();
            LoadTemplates();
        }

        public MessageTemplates()
        {
            templates = new Dictionary<string, MessageTemplate>();
            CreateDefaultTemplates();
        }

        private void CreateDefaultTemplates()
        {
            var invoiceTemplate = new MessageTemplate
            {
                Name = "Invoice",
                Content = "Dear {CustomerName},\n\nYour invoice #{InvoiceNumber} for amount {Amount} has been generated.\n\nThank you for your business!"
            };
            invoiceTemplate.Placeholders.Add("CustomerName", "Customer name");
            invoiceTemplate.Placeholders.Add("InvoiceNumber", "Invoice number");
            invoiceTemplate.Placeholders.Add("Amount", "Invoice amount");
            templates.Add("Invoice", invoiceTemplate);

            var receiptTemplate = new MessageTemplate
            {
                Name = "Receipt",
                Content = "Dear {CustomerName},\n\nPayment received for invoice #{InvoiceNumber}.\nAmount: {Amount}\nDate: {Date}\n\nThank you!"
            };
            receiptTemplate.Placeholders.Add("CustomerName", "Customer name");
            receiptTemplate.Placeholders.Add("InvoiceNumber", "Invoice number");
            receiptTemplate.Placeholders.Add("Amount", "Payment amount");
            receiptTemplate.Placeholders.Add("Date", "Payment date");
            templates.Add("Receipt", receiptTemplate);

            var reminderTemplate = new MessageTemplate
            {
                Name = "Reminder",
                Content = "Dear {CustomerName},\n\nThis is a reminder that invoice #{InvoiceNumber} for {Amount} is due on {DueDate}.\n\nPlease make the payment at your earliest convenience."
            };
            reminderTemplate.Placeholders.Add("CustomerName", "Customer name");
            reminderTemplate.Placeholders.Add("InvoiceNumber", "Invoice number");
            reminderTemplate.Placeholders.Add("Amount", "Invoice amount");
            reminderTemplate.Placeholders.Add("DueDate", "Due date");
            templates.Add("Reminder", reminderTemplate);
        }

        private void LoadTemplates()
        {
            if (!File.Exists(templatesFilePath))
            {
                CreateDefaultTemplates();
                SaveTemplates();
                return;
            }

            try
            {
                XmlDocument doc = new XmlDocument();
                doc.Load(templatesFilePath);

                XmlNodeList templateNodes = doc.SelectNodes("/MessageTemplates/Template");
                
                foreach (XmlNode node in templateNodes)
                {
                    var template = new MessageTemplate();
                    template.Name = node.Attributes["name"].Value;
                    template.Content = node.SelectSingleNode("Content").InnerText;

                    XmlNodeList placeholderNodes = node.SelectNodes("Placeholders/Placeholder");
                    foreach (XmlNode phNode in placeholderNodes)
                    {
                        string key = phNode.Attributes["key"].Value;
                        string description = phNode.InnerText;
                        template.Placeholders.Add(key, description);
                    }

                    templates.Add(template.Name, template);
                }
            }
            catch (Exception ex)
            {
                throw new InvalidOperationException("Failed to load message templates: " + ex.Message, ex);
            }
        }

        public void SaveTemplates()
        {
            if (string.IsNullOrEmpty(templatesFilePath))
            {
                return;
            }

            try
            {
                string directory = Path.GetDirectoryName(templatesFilePath);
                if (!string.IsNullOrEmpty(directory) && !Directory.Exists(directory))
                {
                    Directory.CreateDirectory(directory);
                }

                XmlDocument doc = new XmlDocument();
                XmlDeclaration declaration = doc.CreateXmlDeclaration("1.0", "utf-8", null);
                doc.AppendChild(declaration);

                XmlElement root = doc.CreateElement("MessageTemplates");
                doc.AppendChild(root);

                foreach (var kvp in templates)
                {
                    XmlElement templateElement = doc.CreateElement("Template");
                    templateElement.SetAttribute("name", kvp.Value.Name);

                    XmlElement contentElement = doc.CreateElement("Content");
                    contentElement.InnerText = kvp.Value.Content;
                    templateElement.AppendChild(contentElement);

                    XmlElement placeholdersElement = doc.CreateElement("Placeholders");
                    foreach (var ph in kvp.Value.Placeholders)
                    {
                        XmlElement placeholderElement = doc.CreateElement("Placeholder");
                        placeholderElement.SetAttribute("key", ph.Key);
                        placeholderElement.InnerText = ph.Value;
                        placeholdersElement.AppendChild(placeholderElement);
                    }
                    templateElement.AppendChild(placeholdersElement);

                    root.AppendChild(templateElement);
                }

                doc.Save(templatesFilePath);
            }
            catch (Exception ex)
            {
                throw new InvalidOperationException("Failed to save message templates: " + ex.Message, ex);
            }
        }

        public MessageTemplate GetTemplate(string name)
        {
            if (templates.ContainsKey(name))
            {
                return templates[name];
            }
            return null;
        }

        public void AddTemplate(MessageTemplate template)
        {
            if (string.IsNullOrEmpty(template.Name))
            {
                throw new ArgumentException("Template name cannot be empty");
            }

            if (templates.ContainsKey(template.Name))
            {
                templates[template.Name] = template;
            }
            else
            {
                templates.Add(template.Name, template);
            }
        }

        public bool RemoveTemplate(string name)
        {
            return templates.Remove(name);
        }

        public List<string> GetTemplateNames()
        {
            return new List<string>(templates.Keys);
        }

        public int Count
        {
            get { return templates.Count; }
        }
    }
}
