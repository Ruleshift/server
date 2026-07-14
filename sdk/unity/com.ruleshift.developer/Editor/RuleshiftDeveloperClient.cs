using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Newtonsoft.Json;
using Newtonsoft.Json.Linq;

namespace Ruleshift.Developer
{
    public sealed class RuleshiftDeveloperClient : IDisposable
    {
        private const int MaxResponseCharacters = 4 * 1024 * 1024;
        private readonly HttpClient _http;
        private readonly bool _ownsHttpClient;

        public RuleshiftDeveloperClient(string baseUrl, string developerApiKey, HttpClient httpClient = null)
        {
            if (string.IsNullOrWhiteSpace(baseUrl))
                throw new ArgumentException("Ruleshift base URL must not be empty.", nameof(baseUrl));
            if (string.IsNullOrWhiteSpace(developerApiKey))
                throw new ArgumentException("Ruleshift developer API key must not be empty.", nameof(developerApiKey));

            _ownsHttpClient = httpClient == null;
            _http = httpClient ?? new HttpClient();
            _http.BaseAddress = new Uri(baseUrl.TrimEnd('/') + "/", UriKind.Absolute);
            _http.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", developerApiKey);
            _http.DefaultRequestHeaders.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));
        }

        public Task<RowsPage> ListRowsAsync(
            string moduleKey,
            string table,
            int limit = 100,
            int offset = 0,
            CancellationToken cancellationToken = default)
        {
            var path = $"v2/developer/modules/{Escape(moduleKey)}/tables/{Escape(table)}/rows?limit={limit}&offset={offset}";
            return SendAsync<RowsPage>(HttpMethod.Get, path, null, cancellationToken);
        }

        public Task<RowInfo> CreateRowAsync(
            string moduleKey,
            string table,
            Dictionary<string, object> values,
            CancellationToken cancellationToken = default)
        {
            var path = $"v2/developer/modules/{Escape(moduleKey)}/tables/{Escape(table)}/rows";
            return SendAsync<RowInfo>(HttpMethod.Post, path, new CreateRowRequest { Values = values }, cancellationToken);
        }

        public async Task<ModuleVersionInfo> PublishModuleVersionAsync(
            PublishModuleVersionRequest value,
            CancellationToken cancellationToken = default)
        {
            if (value == null) throw new ArgumentNullException(nameof(value));
            using var content = new MultipartFormDataContent();
            content.Add(new StringContent(JsonConvert.SerializeObject(value.Manifest), Encoding.UTF8, "application/json"), "manifest");
            content.Add(new ByteArrayContent(value.DescriptorSet ?? Array.Empty<byte>()), "descriptor_set", "descriptor.pb");
            content.Add(new ByteArrayContent(value.ConformanceVectors ?? Array.Empty<byte>()), "conformance_vectors", "vectors.json");
            content.Add(new StringContent(value.OciReference ?? string.Empty), "oci_reference");
            if (!string.IsNullOrWhiteSpace(value.RegistryCredential))
                content.Add(new StringContent(value.RegistryCredential), "registry_credential");
            return await SendContentAsync<ModuleVersionInfo>(HttpMethod.Post,
                $"v2/developer/modules/{Escape(value.ModuleId)}/versions", content, cancellationToken);
        }

        public Task<RuntimeModuleInfo> CreateRuntimeModuleAsync(string key, string displayName, CancellationToken cancellationToken = default)
        {
            return SendAsync<RuntimeModuleInfo>(HttpMethod.Post, "v2/developer/modules",
                new { key, display_name = displayName }, cancellationToken);
        }

        public Task<ModuleVersionInfo> GetModuleVersionAsync(string moduleId, string version, CancellationToken cancellationToken = default)
        {
            return SendAsync<ModuleVersionInfo>(HttpMethod.Get,
                $"v2/developer/modules/{Escape(moduleId)}/versions/{Escape(version)}", null, cancellationToken);
        }

        public Task<ValidationStatusInfo> GetValidationStatusAsync(string moduleId, string version, CancellationToken cancellationToken = default)
        {
            return SendAsync<ValidationStatusInfo>(HttpMethod.Get,
                $"v2/developer/modules/{Escape(moduleId)}/versions/{Escape(version)}/validation", null, cancellationToken);
        }

        public Task<RoomInfo> CreateRoomAsync(string moduleId, string version = null, CancellationToken cancellationToken = default)
        {
            return SendAsync<RoomInfo>(HttpMethod.Post, "v2/rooms",
                new CreateRoomV2Request { ModuleId = moduleId, Version = version }, cancellationToken);
        }

        public Task<RoomInfo> GetRoomAsync(string roomId, CancellationToken cancellationToken = default)
        {
            return SendAsync<RoomInfo>(HttpMethod.Get, $"v2/rooms/{Escape(roomId)}", null, cancellationToken);
        }

        public void Dispose()
        {
            if (_ownsHttpClient) _http.Dispose();
        }

        private async Task<T> SendAsync<T>(HttpMethod method, string path, object body, CancellationToken cancellationToken)
        {
            using var request = new HttpRequestMessage(method, path);
            if (body != null)
            {
                request.Content = new StringContent(JsonConvert.SerializeObject(body), Encoding.UTF8, "application/json");
            }

            using var response = await _http.SendAsync(request, HttpCompletionOption.ResponseContentRead, cancellationToken);
            var payload = await response.Content.ReadAsStringAsync();
            if (payload.Length > MaxResponseCharacters)
                throw new RuleshiftApiException(response.StatusCode, "response_too_large", "Ruleshift response is too large.");

            if (!response.IsSuccessStatusCode)
            {
                var error = string.IsNullOrWhiteSpace(payload)
                    ? new ApiError { Code = "request_failed", Message = response.ReasonPhrase }
                    : JsonConvert.DeserializeObject<ApiError>(payload);
                throw new RuleshiftApiException(response.StatusCode, error?.Code, error?.Message);
            }

            return JsonConvert.DeserializeObject<T>(payload);
        }

        private async Task<T> SendContentAsync<T>(HttpMethod method, string path, HttpContent content, CancellationToken cancellationToken)
        {
            using var request = new HttpRequestMessage(method, path) { Content = content };
            using var response = await _http.SendAsync(request, HttpCompletionOption.ResponseContentRead, cancellationToken);
            var payload = await response.Content.ReadAsStringAsync();
            if (!response.IsSuccessStatusCode)
            {
                var error = JsonConvert.DeserializeObject<ApiError>(payload);
                throw new RuleshiftApiException(response.StatusCode, error?.Code, error?.Message);
            }
            return JsonConvert.DeserializeObject<T>(payload);
        }

        private static string Escape(string value)
        {
            if (string.IsNullOrWhiteSpace(value)) throw new ArgumentException("Path value must not be empty.");
            return Uri.EscapeDataString(value);
        }
    }

    public sealed class RuleshiftApiException : Exception
    {
        public HttpStatusCode StatusCode { get; }
        public string Code { get; }

        public RuleshiftApiException(HttpStatusCode statusCode, string code, string message)
            : base(message ?? "Ruleshift API request failed.")
        {
            StatusCode = statusCode;
            Code = code ?? "request_failed";
        }
    }

    public static class RuleshiftColumnType
    {
        public const string String = "string";
        public const string Int64 = "int64";
        public const string Float64 = "float64";
        public const string Bool = "bool";
        public const string Timestamp = "timestamp";
        public const string Json = "json";
    }

    public sealed class RowsPage
    {
        [JsonProperty("module")] public string Module { get; set; }
        [JsonProperty("table")] public string Table { get; set; }
        [JsonProperty("columns")] public List<string> Columns { get; set; } = new List<string>();
        [JsonProperty("rows")] public List<Dictionary<string, JToken>> Rows { get; set; } = new List<Dictionary<string, JToken>>();
        [JsonProperty("limit")] public int Limit { get; set; }
        [JsonProperty("offset")] public int Offset { get; set; }
    }

    public sealed class CreateRowRequest
    {
        [JsonProperty("values")] public Dictionary<string, object> Values { get; set; } = new Dictionary<string, object>();
    }

    public sealed class RowInfo
    {
        [JsonProperty("module")] public string Module { get; set; }
        [JsonProperty("table")] public string Table { get; set; }
        [JsonProperty("values")] public Dictionary<string, JToken> Values { get; set; } = new Dictionary<string, JToken>();
    }

    public sealed class PublishModuleVersionRequest
    {
        public string ModuleId { get; set; }
        public string OciReference { get; set; }
        public string RegistryCredential { get; set; }
        public RuntimeManifest Manifest { get; set; }
        public byte[] DescriptorSet { get; set; }
        public byte[] ConformanceVectors { get; set; }
    }

    public sealed class RuntimeManifest
    {
        [JsonProperty("module_id")] public string ModuleId { get; set; }
        [JsonProperty("version")] public string Version { get; set; }
        [JsonProperty("abi_version")] public uint AbiVersion { get; set; } = 1;
        [JsonProperty("state_type_url")] public string StateTypeUrl { get; set; }
        [JsonProperty("command_type_urls")] public List<string> CommandTypeUrls { get; set; } = new List<string>();
        [JsonProperty("transition_deadline_ms")] public int TransitionDeadlineMs { get; set; }
        [JsonProperty("capabilities")] public List<string> Capabilities { get; set; } = new List<string>();
        [JsonProperty("database_migrations")] public List<DatabaseMigration> DatabaseMigrations { get; set; } = new List<DatabaseMigration>();
    }

    public sealed class DatabaseMigration
    {
        [JsonProperty("version")] public ulong Version { get; set; }
        [JsonProperty("name")] public string Name { get; set; }
        [JsonProperty("tables")] public List<TableDefinition> Tables { get; set; } = new List<TableDefinition>();
    }

    public sealed class TableDefinition
    {
        [JsonProperty("name")] public string Name { get; set; }
        [JsonProperty("columns")] public List<ColumnDefinition> Columns { get; set; } = new List<ColumnDefinition>();
    }

    public sealed class ColumnDefinition
    {
        [JsonProperty("name")] public string Name { get; set; }
        [JsonProperty("type")] public string Type { get; set; }
        [JsonProperty("nullable")] public bool Nullable { get; set; }
        [JsonProperty("primary_key")] public bool PrimaryKey { get; set; }
    }

    public sealed class ModuleReferenceInfo
    {
        [JsonProperty("developer_id")] public string DeveloperId { get; set; }
        [JsonProperty("module_id")] public string ModuleId { get; set; }
        [JsonProperty("version")] public string Version { get; set; }
        [JsonProperty("image_digest")] public string ImageDigest { get; set; }
    }

    public sealed class ModuleVersionInfo
    {
        [JsonProperty("ref")] public ModuleReferenceInfo Ref { get; set; }
        [JsonProperty("image_ref")] public string ImageRef { get; set; }
        [JsonProperty("status")] public string Status { get; set; }
        [JsonProperty("descriptor_digest")] public string DescriptorDigest { get; set; }
        [JsonProperty("manifest")] public RuntimeManifest Manifest { get; set; }
    }

    public sealed class RuntimeModuleInfo
    {
        [JsonProperty("developer_id")] public string DeveloperId { get; set; }
        [JsonProperty("key")] public string Key { get; set; }
        [JsonProperty("display_name")] public string DisplayName { get; set; }
        [JsonProperty("active_version")] public string ActiveVersion { get; set; }
    }

    public sealed class ValidationStatusInfo
    {
        [JsonProperty("result")] public string Result { get; set; }
        [JsonProperty("logs")] public string Logs { get; set; }
        [JsonProperty("started_at")] public DateTimeOffset StartedAt { get; set; }
        [JsonProperty("finished_at")] public DateTimeOffset? FinishedAt { get; set; }
    }

    public sealed class CreateRoomV2Request
    {
        [JsonProperty("module_id")] public string ModuleId { get; set; }
        [JsonProperty("version")] public string Version { get; set; }
    }

    public sealed class RoomInfo
    {
        [JsonProperty("room_id")] public string RoomId { get; set; }
        [JsonProperty("module")] public ModuleReferenceInfo Module { get; set; }
        [JsonProperty("module_database")] public string ModuleDatabase { get; set; }
        [JsonProperty("seed")] public ulong Seed { get; set; }
        [JsonProperty("created_at")] public DateTime CreatedAt { get; set; }
        [JsonProperty("invite_code")] public string InviteCode { get; set; }
        [JsonProperty("invite_deadline")] public DateTime InviteDeadline { get; set; }
    }

    internal sealed class ApiError
    {
        [JsonProperty("code")] public string Code { get; set; }
        [JsonProperty("message")] public string Message { get; set; }
    }
}
