using System;
using System.IO;
using System.Net;
using System.Security.Cryptography;
using System.Text;
using System.Web.Script.Serialization;

namespace TallyWhatsappsender
{
    /// <summary>
    /// HTTP client for the local Go bridge service.
    ///
    /// Two changes vs the legacy implementation:
    ///
    ///  1. Base URL is discovered from HKLM\SOFTWARE\TallyWhatsApp\Port at
    ///     each construction time. The service binds a dynamic port at
    ///     startup, so hardcoding 8080 caused conflicts on machines that
    ///     already had something on 8080.
    ///
    ///  2. Every request is HMAC-signed. The canonical string and
    ///     algorithm match the Go server's loopback.Sign:
    ///
    ///         StringToSign = METHOD + "\n" + PATH + "\n" + TIMESTAMP +
    ///                        "\n" + NONCE + "\n" + hex(sha256(body))
    ///         Signature    = hex(hmac_sha256(secret, StringToSign))
    ///
    ///     Headers:
    ///         X-TallyWA-Timestamp   unix seconds
    ///         X-TallyWA-Nonce       8-byte random hex
    ///         X-TallyWA-Signature   hex of HMAC-SHA256
    ///
    /// Without the signature, the server returns 401 — so this DLL only
    /// works in concert with a running, paired service.
    /// </summary>
    public class WhatsAppClient
    {
        private readonly string _initError; // non-null if config (not handshake) failed
        private readonly int _timeoutMs;
        private readonly int _maxRetries;
        private readonly int _retryDelayMs;
        private static readonly JavaScriptSerializer _json = new JavaScriptSerializer();

        public WhatsAppClient()
        {
            // Defaults; overridden below if config is present.
            _timeoutMs = 30000;
            _maxRetries = 3;
            _retryDelayMs = 1000;

            try
            {
                var settings = TallyWhatsappsender.Config.ConfigManager.Instance.WhatsAppSettings;
                _timeoutMs = settings.ApiTimeout * 1000;
                _maxRetries = settings.MaxRetries;
                _retryDelayMs = settings.RetryDelay;
            }
            catch
            {
                // Config is optional now; registry handshake is the source of truth.
            }
        }

        /// <summary>Send a text message.</summary>
        public ApiResult SendMessage(string recipient, string message)
        {
            string body = _json.Serialize(new { recipient = recipient, message = message });
            return PostWithRetry("/api/send-message", body);
        }

        /// <summary>Send a file (PDF, image) with optional caption.</summary>
        public ApiResult SendFile(string recipient, string filePath, string caption)
        {
            string body = _json.Serialize(new { recipient = recipient, file_path = filePath, caption = caption ?? "" });
            return PostWithRetry("/api/send-file", body);
        }

        /// <summary>Send a file with caption + a follow-up text message.</summary>
        public ApiResult SendFileWithMessage(string recipient, string filePath, string message)
        {
            return SendFileWithMessage(recipient, filePath, message, "sale", null);
        }

        /// <summary>Send a file with voucher-type routing.</summary>
        public ApiResult SendFileWithMessage(string recipient, string filePath, string message, string voucherType)
        {
            return SendFileWithMessage(recipient, filePath, message, voucherType, null);
        }

        /// <summary>
        /// Send a file with voucher-type routing and an explicit idempotency key.
        /// The bridge dedupes on (recipient, idempotency_key) inside a 5-minute
        /// window — required to make a Tally Save double-click safe.
        /// </summary>
        public ApiResult SendFileWithMessage(string recipient, string filePath, string message, string voucherType, string idempotencyKey)
        {
            string body = _json.Serialize(new
            {
                recipient = recipient,
                file_path = filePath,
                message = message,
                voucher_type = voucherType ?? "sale",
                idempotency_key = idempotencyKey ?? ""
            });
            return PostWithRetry("/api/send-file-with-message", body);
        }

        /// <summary>Health check — returns true if WhatsApp is paired and connected.</summary>
        public bool CheckHealth()
        {
            try
            {
                ApiResult result = Get("/api/health");
                if (!result.Success) return false;

                // Parse the body for the authenticated flag.
                var dict = _json.DeserializeObject(result.Message)
                           as System.Collections.Generic.Dictionary<string, object>;
                if (dict == null) return false;
                return dict.ContainsKey("authenticated") && (bool)dict["authenticated"];
            }
            catch
            {
                return false;
            }
        }

        // ─── Private helpers ─────────────────────────────────────────────────

        private ApiResult InitErrorResult(string err)
        {
            return new ApiResult
            {
                Success = false,
                Message = "TallyWhatsApp service unavailable: " + err,
                StatusCode = 0
            };
        }

        private ApiResult PostWithRetry(string endpoint, string jsonBody)
        {
            ApiResult lastResult = null;
            for (int attempt = 1; attempt <= _maxRetries; attempt++)
            {
                try
                {
                    lastResult = Send("POST", endpoint, jsonBody);
                    if (lastResult.Success) return lastResult;

                    // Don't retry on 4xx — those are client errors that won't fix themselves.
                    if (lastResult.StatusCode >= 400 && lastResult.StatusCode < 500)
                        return lastResult;
                }
                catch (WebException ex)
                {
                    lastResult = new ApiResult { Success = false, Message = "Connection error: " + ex.Message, StatusCode = 0 };
                }
                catch (Exception ex)
                {
                    lastResult = new ApiResult { Success = false, Message = "Unexpected error: " + ex.Message, StatusCode = 0 };
                }

                if (attempt < _maxRetries)
                {
                    System.Threading.Thread.Sleep(_retryDelayMs * attempt);
                }
            }
            return lastResult ?? new ApiResult { Success = false, Message = "Unknown error after retries" };
        }

        private ApiResult Get(string endpoint)
        {
            return Send("GET", endpoint, null);
        }

        private ApiResult Send(string method, string endpoint, string jsonBody)
        {
            // Re-read the handshake on every call. The service publishes a
            // dynamic port that changes on each restart; caching would mean
            // a service restart silently breaks Tally until Tally is also
            // restarted. The registry read is sub-millisecond on Windows.
            RegistryHandshake.HandshakeData hs;
            try
            {
                hs = RegistryHandshake.Load();
            }
            catch (Exception ex)
            {
                return InitErrorResult(ex.Message);
            }

            string baseUrl = "http://127.0.0.1:" + hs.Port;
            byte[] secret = hs.Secret;

            string url = baseUrl + endpoint;
            byte[] bodyBytes = jsonBody == null ? new byte[0] : Encoding.UTF8.GetBytes(jsonBody);

            // Compute HMAC over the canonical string.
            long timestamp = (long)(DateTime.UtcNow - new DateTime(1970, 1, 1, 0, 0, 0, DateTimeKind.Utc)).TotalSeconds;
            string nonce = NewNonce();
            string signature = ComputeSignature(secret, method, endpoint, timestamp, nonce, bodyBytes);

            var request = (HttpWebRequest)WebRequest.Create(url);
            request.Method = method;
            request.Timeout = _timeoutMs;
            request.Headers.Add("X-TallyWA-Timestamp", timestamp.ToString());
            request.Headers.Add("X-TallyWA-Nonce", nonce);
            request.Headers.Add("X-TallyWA-Signature", signature);

            if (jsonBody != null)
            {
                request.ContentType = "application/json";
                request.ContentLength = bodyBytes.Length;
                using (var stream = request.GetRequestStream())
                {
                    stream.Write(bodyBytes, 0, bodyBytes.Length);
                }
            }

            HttpWebResponse response;
            try
            {
                response = (HttpWebResponse)request.GetResponse();
            }
            catch (WebException ex)
            {
                var errorResp = ex.Response as HttpWebResponse;
                if (errorResp != null)
                {
                    string errorBody;
                    using (var reader = new StreamReader(errorResp.GetResponseStream()))
                        errorBody = reader.ReadToEnd();
                    return ParseResponse(errorBody, (int)errorResp.StatusCode);
                }
                throw;
            }

            using (response)
            using (var reader = new StreamReader(response.GetResponseStream()))
            {
                string body = reader.ReadToEnd();
                return ParseResponse(body, (int)response.StatusCode);
            }
        }

        /// <summary>
        /// Mirrors loopback.Sign in the Go service. Any change here must
        /// be applied identically server-side or every signature mismatches.
        /// </summary>
        private string ComputeSignature(byte[] secret, string method, string path, long timestamp, string nonce, byte[] body)
        {
            byte[] bodyHash;
            using (var sha = SHA256.Create())
            {
                bodyHash = sha.ComputeHash(body);
            }

            string stringToSign = method + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + ToHex(bodyHash);

            using (var hmac = new HMACSHA256(secret))
            {
                byte[] mac = hmac.ComputeHash(Encoding.UTF8.GetBytes(stringToSign));
                return ToHex(mac);
            }
        }

        private static readonly RNGCryptoServiceProvider _rng = new RNGCryptoServiceProvider();

        private static string NewNonce()
        {
            byte[] buf = new byte[8];
            _rng.GetBytes(buf);
            return ToHex(buf);
        }

        private static string ToHex(byte[] bytes)
        {
            var sb = new StringBuilder(bytes.Length * 2);
            for (int i = 0; i < bytes.Length; i++)
            {
                sb.Append(bytes[i].ToString("x2"));
            }
            return sb.ToString();
        }

        private ApiResult ParseResponse(string body, int statusCode)
        {
            try
            {
                var dict = _json.DeserializeObject(body) as System.Collections.Generic.Dictionary<string, object>;
                if (dict != null)
                {
                    bool success = dict.ContainsKey("success") && (bool)dict["success"];
                    string message = dict.ContainsKey("message") && dict["message"] != null ? dict["message"].ToString() : "";
                    string error = dict.ContainsKey("error") && dict["error"] != null ? dict["error"].ToString() : "";
                    string msgId = dict.ContainsKey("message_id") && dict["message_id"] != null ? dict["message_id"].ToString() : "";

                    return new ApiResult
                    {
                        Success = success || (string.IsNullOrEmpty(error) && statusCode >= 200 && statusCode < 300),
                        Message = !string.IsNullOrEmpty(error) ? error : (string.IsNullOrEmpty(message) ? body : message),
                        MessageId = msgId,
                        StatusCode = statusCode
                    };
                }
            }
            catch { /* fall through */ }

            return new ApiResult
            {
                Success = statusCode >= 200 && statusCode < 300,
                Message = body,
                StatusCode = statusCode
            };
        }
    }

    /// <summary>Result of a WhatsApp API call.</summary>
    public class ApiResult
    {
        public bool Success { get; set; }
        public string Message { get; set; }
        public string MessageId { get; set; }
        public int StatusCode { get; set; }
    }
}
