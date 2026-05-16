using System;
using System.Runtime.InteropServices;

namespace TallyWhatsappsender
{
    /// <summary>
    /// COM-visible interface for Tally WhatsApp integration
    /// Maintains backward compatibility while adding new features
    /// </summary>
    [ComVisible(true)]
    [Guid("2B8E9F1C-A3D4-4F2B-9C7E-1234567890AB")]
    [InterfaceType(ComInterfaceType.InterfaceIsIDispatch)]
    public interface ITallyWhatsAppSender
    {
        /// <summary>
        /// Legacy method - maintains backward compatibility with existing TDL scripts
        /// </summary>
        [DispId(1)]
        string InitProcess(string contact, string file_route, string title, string chrome_binary);

        /// <summary>
        /// Typed entrypoint for voucher routing in the bridge.
        /// </summary>
        [DispId(8)]
        string InitProcessWithType(string contact, string file_route, string title, string chrome_binary, string voucher_type);

        /// <summary>
        /// Voucher-aware entrypoint used by the new TDL files. The
        /// idempotency_key is the Tally voucher GUID, which makes a
        /// double-press of Save safe — the bridge dedupes inside a
        /// 5-minute window. Fire-and-forget; returns immediately.
        /// </summary>
        [DispId(9)]
        string SendVoucher(string contact, string file_path, string message, string voucher_type, string idempotency_key);

        /// <summary>
        /// Send message with file attachment using new HTTP API
        /// </summary>
        [DispId(2)]
        string SendFileWithMessage(string contact, string file_path, string message);

        /// <summary>
        /// Send text message only
        /// </summary>
        [DispId(3)]
        string SendMessage(string contact, string message);

        /// <summary>
        /// Send file with caption
        /// </summary>
        [DispId(4)]
        string SendFile(string contact, string file_path, string caption);

        /// <summary>
        /// Send message using template
        /// </summary>
        [DispId(5)]
        string SendTemplateMessage(string contact, string templateName, string templateData);

        /// <summary>
        /// Check if WhatsApp service is connected
        /// </summary>
        [DispId(6)]
        bool IsServiceAvailable();

        /// <summary>
        /// Get last error message
        /// </summary>
        [DispId(7)]
        string GetLastError();
    }

    /// <summary>
    /// Main implementation class for Tally WhatsApp integration
    /// Uses HTTP API to communicate with Go WhatsApp bridge
    /// </summary>
    [ComVisible(true)]
    [Guid("3C9F0E2D-B4E5-4A3C-0D8F-2345678901BC")]
    [ClassInterface(ClassInterfaceType.None)]
    [ProgId("TallyWhatsappsender.WhatsAppSender")]
    public class WhatsAppSender : ITallyWhatsAppSender
    {
        private string _lastError;
        private readonly WhatsAppClient _client;

        public WhatsAppSender()
        {
            _lastError = string.Empty;
            _client = new WhatsAppClient();
        }

        /// <summary>
        /// Legacy method for backward compatibility with old TDL scripts
        /// Now uses HTTP API instead of Selenium
        /// </summary>
        public string InitProcess(string contact, string file_route, string title, string chrome_binary)
        {
            return InitProcessWithType(contact, file_route, title, chrome_binary, "sale");
        }

        /// <summary>
        /// Voucher-aware entrypoint that lets the bridge route sales, receipts, and ledger requests.
        /// </summary>
        public string InitProcessWithType(string contact, string file_route, string title, string chrome_binary, string voucher_type)
        {
            try
            {
                if (string.IsNullOrEmpty(contact))
                {
                    return "Error: Contact number is required";
                }
                if (string.IsNullOrEmpty(file_route))
                {
                    return "Error: File path is required";
                }

                System.Threading.ThreadPool.QueueUserWorkItem(state => {
                    SendFileWithMessage(contact, file_route, title, NormalizeVoucherType(voucher_type));
                });

                return "Message queued for sending in background.";
            }
            catch (Exception ex)
            {
                _lastError = ex.Message;
                return "Error: " + ex.Message;
            }
        }

        /// <summary>
        /// Voucher entrypoint used by the new TDL files. Forwards the
        /// idempotency key (Tally voucher GUID) to the bridge so a
        /// double-press of Save is safe. Fire-and-forget — returns
        /// immediately so Tally never freezes on a slow WhatsApp send.
        /// </summary>
        public string SendVoucher(string contact, string file_path, string message, string voucher_type, string idempotency_key)
        {
            try
            {
                if (string.IsNullOrEmpty(contact))
                {
                    return "Error: Contact number is required";
                }
                if (string.IsNullOrEmpty(file_path))
                {
                    return "Error: File path is required";
                }

                string vt = NormalizeVoucherType(voucher_type);
                string idem = idempotency_key ?? "";

                System.Threading.ThreadPool.QueueUserWorkItem(state => {
                    try
                    {
                        SendFileWithMessage(contact, file_path, message, vt, idem);
                    }
                    catch (Exception ex)
                    {
                        _lastError = ex.Message;
                    }
                });

                return "Queued. WhatsApp delivery runs in the background.";
            }
            catch (Exception ex)
            {
                _lastError = ex.Message;
                return "Error: " + ex.Message;
            }
        }

        /// <summary>
        /// Send file with message to one or more contacts
        /// </summary>
        public string SendFileWithMessage(string contact, string file_path, string message)
        {
            return SendFileWithMessage(contact, file_path, message, "sale", null);
        }

        /// <summary>
        /// Send file with message to one or more contacts with voucher routing.
        /// </summary>
        public string SendFileWithMessage(string contact, string file_path, string message, string voucher_type)
        {
            return SendFileWithMessage(contact, file_path, message, voucher_type, null);
        }

        /// <summary>
        /// Send file with message + voucher routing + idempotency key.
        /// The bridge dedupes on (recipient, idempotency_key) within a
        /// 5-minute window — required for double-press-of-Save safety.
        /// </summary>
        public string SendFileWithMessage(string contact, string file_path, string message, string voucher_type, string idempotency_key)
        {
            try
            {
                if (!System.IO.File.Exists(file_path))
                {
                    _lastError = "File not found: " + file_path;
                    return "Error: Attachment not found!";
                }

                string[] contacts = contact.Split(new[] { ',' }, StringSplitOptions.RemoveEmptyEntries);
                int successCount = 0;
                int failCount = 0;
                string lastError = "";
                string vt = NormalizeVoucherType(voucher_type);

                foreach (string ct in contacts)
                {
                    string cleanContact = ct.Trim();

                    if (cleanContact.Length != 12 || !IsNumeric(cleanContact))
                    {
                        failCount++;
                        lastError = "Invalid contact number: " + cleanContact;
                        continue;
                    }

                    // Per-recipient idempotency key. If the caller supplied a
                    // voucher GUID we suffix the contact so two parties on the
                    // same voucher both receive their copy (otherwise the
                    // bridge would dedupe the second send).
                    string perRecipientKey = string.IsNullOrEmpty(idempotency_key)
                        ? null
                        : idempotency_key + ":" + cleanContact;

                    var result = _client.SendFileWithMessage(cleanContact, file_path, message, vt, perRecipientKey);
                    if (!result.Success)
                    {
                        failCount++;
                        lastError = result.Message;
                        continue;
                    }

                    successCount++;
                }

                if (failCount > 0)
                {
                    _lastError = lastError;
                    return "Partial success: " + successCount + " sent, " + failCount + " failed. Last error: " + lastError;
                }

                return "Process finished: " + successCount + " message(s) sent successfully";
            }
            catch (Exception ex)
            {
                _lastError = ex.Message;
                return "Error: " + ex.Message;
            }
        }

        /// <summary>
        /// Send text message only
        /// </summary>
        public string SendMessage(string contact, string message)
        {
            try
            {
                if (string.IsNullOrEmpty(contact))
                {
                    return "Error: Contact number is required";
                }

                if (string.IsNullOrEmpty(message))
                {
                    return "Error: Message is required";
                }

                // Split contacts for multiple recipients
                string[] contacts = contact.Split(new[] { ',' }, StringSplitOptions.RemoveEmptyEntries);
                int successCount = 0;
                int failCount = 0;
                string lastError = "";

                foreach (string ct in contacts)
                {
                    string cleanContact = ct.Trim();
                    
                    if (cleanContact.Length != 12 || !IsNumeric(cleanContact))
                    {
                        failCount++;
                        lastError = "Invalid contact number: " + cleanContact;
                        continue;
                    }

                    var result = _client.SendMessage(cleanContact, message);
                    if (result.Success)
                    {
                        successCount++;
                    }
                    else
                    {
                        failCount++;
                        lastError = result.Message;
                    }
                }

                if (failCount > 0)
                {
                    _lastError = lastError;
                    return "Partial success: " + successCount + " sent, " + failCount + " failed";
                }

                return "Success: " + successCount + " message(s) sent";
            }
            catch (Exception ex)
            {
                _lastError = ex.Message;
                return "Error: " + ex.Message;
            }
        }

        /// <summary>
        /// Send file with optional caption
        /// </summary>
        public string SendFile(string contact, string file_path, string caption)
        {
            try
            {
                if (!System.IO.File.Exists(file_path))
                {
                    _lastError = "File not found: " + file_path;
                    return "Error: File not found!";
                }

                string[] contacts = contact.Split(new[] { ',' }, StringSplitOptions.RemoveEmptyEntries);
                int successCount = 0;
                int failCount = 0;
                string lastError = "";

                foreach (string ct in contacts)
                {
                    string cleanContact = ct.Trim();
                    
                    if (cleanContact.Length != 12 || !IsNumeric(cleanContact))
                    {
                        failCount++;
                        lastError = "Invalid contact: " + cleanContact;
                        continue;
                    }

                    var result = _client.SendFile(cleanContact, file_path, caption);
                    if (result.Success)
                    {
                        successCount++;
                    }
                    else
                    {
                        failCount++;
                        lastError = result.Message;
                    }
                }

                if (failCount > 0)
                {
                    _lastError = lastError;
                    return "Partial success: " + successCount + " sent, " + failCount + " failed";
                }

                return "Success: " + successCount + " file(s) sent";
            }
            catch (Exception ex)
            {
                _lastError = ex.Message;
                return "Error: " + ex.Message;
            }
        }

        /// <summary>
        /// Send message using predefined template
        /// </summary>
        public string SendTemplateMessage(string contact, string templateName, string templateData)
        {
            try
            {
                var template = MessageTemplates.GetTemplate(templateName);
                if (template == null)
                {
                    return "Error: Template '" + templateName + "' not found";
                }

                string message = template.ApplyData(templateData);
                return SendMessage(contact, message);
            }
            catch (Exception ex)
            {
                _lastError = ex.Message;
                return "Error: " + ex.Message;
            }
        }

        /// <summary>
        /// Check if WhatsApp service is available
        /// </summary>
        public bool IsServiceAvailable()
        {
            try
            {
                return _client.CheckHealth();
            }
            catch
            {
                return false;
            }
        }

        /// <summary>
        /// Get last error message
        /// </summary>
        public string GetLastError()
        {
            return _lastError;
        }

        private bool IsNumeric(string value)
        {
            foreach (char c in value)
            {
                if (!char.IsDigit(c))
                    return false;
            }
            return true;
        }

        private string NormalizeVoucherType(string voucherType)
        {
            if (string.IsNullOrEmpty(voucherType))
            {
                return "sale";
            }

            string normalized = voucherType.Trim().ToLowerInvariant();
            if (normalized == "receipt" || normalized == "ledger" || normalized == "sale")
            {
                return normalized;
            }

            return "sale";
        }
    }
}
