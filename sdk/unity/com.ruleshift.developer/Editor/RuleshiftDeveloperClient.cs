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

        public Task<ModuleInfo> CreateModuleAsync(CreateModuleRequest request, CancellationToken cancellationToken = default)
        {
            if (request == null) throw new ArgumentNullException(nameof(request));
            return SendAsync<ModuleInfo>(HttpMethod.Post, "v1/developer/modules", request, cancellationToken);
        }

        public async Task<IReadOnlyList<ModuleInfo>> ListModulesAsync(CancellationToken cancellationToken = default)
        {
            var response = await SendAsync<ModuleList>(HttpMethod.Get, "v1/developer/modules", null, cancellationToken);
            return response.Modules;
        }

        public Task<ModuleSchemaInfo> GetSchemaAsync(string moduleKey, CancellationToken cancellationToken = default)
        {
            return SendAsync<ModuleSchemaInfo>(HttpMethod.Get,
                $"v1/developer/modules/{Escape(moduleKey)}/schema", null, cancellationToken);
        }

        public Task<RowsPage> ListRowsAsync(
            string moduleKey,
            string table,
            int limit = 100,
            int offset = 0,
            CancellationToken cancellationToken = default)
        {
            var path = $"v1/developer/modules/{Escape(moduleKey)}/tables/{Escape(table)}/rows?limit={limit}&offset={offset}";
            return SendAsync<RowsPage>(HttpMethod.Get, path, null, cancellationToken);
        }

        public Task<RowInfo> CreateRowAsync(
            string moduleKey,
            string table,
            Dictionary<string, object> values,
            CancellationToken cancellationToken = default)
        {
            var path = $"v1/developer/modules/{Escape(moduleKey)}/tables/{Escape(table)}/rows";
            return SendAsync<RowInfo>(HttpMethod.Post, path, new CreateRowRequest { Values = values }, cancellationToken);
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

    public sealed class CreateModuleRequest
    {
        [JsonProperty("key")] public string Key { get; set; }
        [JsonProperty("display_name")] public string DisplayName { get; set; }
        [JsonProperty("schema")] public ModuleSchema Schema { get; set; } = new ModuleSchema();
    }

    public sealed class ModuleSchema
    {
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

    public sealed class ModuleInfo
    {
        [JsonProperty("key")] public string Key { get; set; }
        [JsonProperty("display_name")] public string DisplayName { get; set; }
        [JsonProperty("game_type")] public byte GameType { get; set; }
        [JsonProperty("created_at")] public DateTimeOffset CreatedAt { get; set; }
    }

    public sealed class ModuleList
    {
        [JsonProperty("modules")] public List<ModuleInfo> Modules { get; set; } = new List<ModuleInfo>();
    }

    public sealed class ModuleSchemaInfo
    {
        [JsonProperty("module")] public string Module { get; set; }
        [JsonProperty("tables")] public List<TableSchemaInfo> Tables { get; set; } = new List<TableSchemaInfo>();
    }

    public sealed class TableSchemaInfo
    {
        [JsonProperty("name")] public string Name { get; set; }
        [JsonProperty("columns")] public List<ColumnSchemaInfo> Columns { get; set; } = new List<ColumnSchemaInfo>();
    }

    public sealed class ColumnSchemaInfo
    {
        [JsonProperty("name")] public string Name { get; set; }
        [JsonProperty("sql_type")] public string SqlType { get; set; }
        [JsonProperty("nullable")] public bool Nullable { get; set; }
        [JsonProperty("primary_key")] public bool PrimaryKey { get; set; }
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

    internal sealed class ApiError
    {
        [JsonProperty("code")] public string Code { get; set; }
        [JsonProperty("message")] public string Message { get; set; }
    }
}
