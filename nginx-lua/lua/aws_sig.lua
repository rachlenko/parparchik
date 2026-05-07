-- aws_sig.lua — AWS Signature V4 implementation using OpenResty crypto.

local resty_sha256 = require "resty.sha256"
local str          = require "resty.string"
local ffi          = require "ffi"

local _M = {}

-- ---------------------------------------------------------------------------
-- Helpers
-- ---------------------------------------------------------------------------

local function sha256_hex(data)
    local sha = resty_sha256:new()
    sha:update(data or "")
    return str.to_hex(sha:final())
end

-- Use OpenSSL HMAC via FFI since ngx.hmac_sha256 may not be available
ffi.cdef[[
    typedef struct engine_st ENGINE;
    typedef struct evp_md_st EVP_MD;
    typedef struct hmac_ctx_st HMAC_CTX;
    const EVP_MD *EVP_sha256(void);
    unsigned char *HMAC(const EVP_MD *evp_md,
                        const void *key, int key_len,
                        const unsigned char *d, size_t n,
                        unsigned char *md, unsigned int *md_len);
]]

local function hmac_sha256_raw(key, data)
    local evp = ffi.C.EVP_sha256()
    local buf = ffi.new("unsigned char[32]")
    local md_len = ffi.new("unsigned int[1]")
    ffi.C.HMAC(evp, key, #key, data, #data, buf, md_len)
    return ffi.string(buf, 32)
end

local function hmac_sha256_hex(key, data)
    return str.to_hex(hmac_sha256_raw(key, data))
end

-- URI-encode a string per AWS rules (RFC 3986, but / is also encoded unless
-- caller opts out).
local function uri_encode(s, encode_slash)
    if encode_slash == nil then encode_slash = true end
    local out = {}
    for i = 1, #s do
        local c = s:sub(i, i)
        if c:match("[A-Za-z0-9_.~-]") then
            out[#out + 1] = c
        elseif c == "/" and not encode_slash then
            out[#out + 1] = "/"
        else
            out[#out + 1] = string.format("%%%02X", c:byte())
        end
    end
    return table.concat(out)
end

-- ---------------------------------------------------------------------------
-- Signing key derivation
-- ---------------------------------------------------------------------------

local function derive_signing_key(secret_key, date_stamp, region, service)
    local k_date    = hmac_sha256_raw("AWS4" .. secret_key, date_stamp)
    local k_region  = hmac_sha256_raw(k_date, region)
    local k_service = hmac_sha256_raw(k_region, service)
    local k_signing = hmac_sha256_raw(k_service, "aws4_request")
    return k_signing
end

-- ---------------------------------------------------------------------------
-- Sign a request (returns table of headers to add)
-- ---------------------------------------------------------------------------

--- Sign an HTTP request with AWS Signature V4.
-- @param opts table with fields:
--   method, path, query (string), headers (table), body (string),
--   region, service, access_key, secret_key, timestamp (os.time())
-- @return table of headers to merge into the request
function _M.sign_request(opts)
    local method     = opts.method or "GET"
    local path       = opts.path or "/"
    local query      = opts.query or ""
    local headers    = opts.headers or {}
    local body       = opts.body or ""
    local region     = opts.region or "us-east-1"
    local service    = opts.service or "s3"
    local access_key = opts.access_key
    local secret_key = opts.secret_key

    local now = opts.timestamp or os.time()
    local amz_date   = os.date("!%Y%m%dT%H%M%SZ", now)
    local date_stamp = os.date("!%Y%m%d", now)

    -- Ensure host header is in the headers table
    headers["x-amz-date"] = amz_date
    headers["x-amz-content-sha256"] = sha256_hex(body)

    -- Build canonical headers and signed headers
    local hdr_keys = {}
    local hdr_map  = {}
    for k, v in pairs(headers) do
        local lk = k:lower()
        hdr_map[lk] = tostring(v):gsub("^%s+", ""):gsub("%s+$", "")
        hdr_keys[#hdr_keys + 1] = lk
    end
    table.sort(hdr_keys)

    local canonical_headers = {}
    for _, k in ipairs(hdr_keys) do
        canonical_headers[#canonical_headers + 1] = k .. ":" .. hdr_map[k]
    end
    local canonical_headers_str = table.concat(canonical_headers, "\n") .. "\n"
    local signed_headers = table.concat(hdr_keys, ";")

    -- Build canonical query string
    local canonical_query = ""
    if query and query ~= "" then
        local params = {}
        for pair in query:gmatch("[^&]+") do
            local k, v = pair:match("^([^=]+)=?(.*)")
            params[#params + 1] = { uri_encode(k, true), uri_encode(v, true) }
        end
        table.sort(params, function(a, b) return a[1] < b[1] end)
        local parts = {}
        for _, p in ipairs(params) do
            parts[#parts + 1] = p[1] .. "=" .. p[2]
        end
        canonical_query = table.concat(parts, "&")
    end

    local canonical_request = table.concat({
        method,
        uri_encode(path, false),
        canonical_query,
        canonical_headers_str,
        signed_headers,
        sha256_hex(body),
    }, "\n")

    local credential_scope = date_stamp .. "/" .. region .. "/" .. service .. "/aws4_request"

    local string_to_sign = table.concat({
        "AWS4-HMAC-SHA256",
        amz_date,
        credential_scope,
        sha256_hex(canonical_request),
    }, "\n")

    local signing_key = derive_signing_key(secret_key, date_stamp, region, service)
    local signature   = hmac_sha256_hex(signing_key, string_to_sign)

    local auth_header = "AWS4-HMAC-SHA256 Credential=" .. access_key .. "/" .. credential_scope
        .. ", SignedHeaders=" .. signed_headers
        .. ", Signature=" .. signature

    return {
        ["Authorization"]        = auth_header,
        ["x-amz-date"]           = amz_date,
        ["x-amz-content-sha256"] = headers["x-amz-content-sha256"],
    }
end

-- ---------------------------------------------------------------------------
-- Generate a presigned URL (query-string signing)
-- ---------------------------------------------------------------------------

function _M.presigned_url(opts)
    local method     = "GET"
    local host       = opts.host
    local path       = opts.path or "/"
    local region     = opts.region or "us-east-1"
    local service    = opts.service or "s3"
    local access_key = opts.access_key
    local secret_key = opts.secret_key
    local expires    = opts.expires or 3600
    local scheme     = opts.scheme or "http"

    local now = opts.timestamp or os.time()
    local amz_date   = os.date("!%Y%m%dT%H%M%SZ", now)
    local date_stamp = os.date("!%Y%m%d", now)

    local credential = access_key .. "/" .. date_stamp .. "/" .. region .. "/" .. service .. "/aws4_request"

    local canonical_query_params = {
        "X-Amz-Algorithm=AWS4-HMAC-SHA256",
        "X-Amz-Credential=" .. uri_encode(credential, true),
        "X-Amz-Date=" .. amz_date,
        "X-Amz-Expires=" .. tostring(expires),
        "X-Amz-SignedHeaders=host",
    }
    table.sort(canonical_query_params)
    local canonical_query = table.concat(canonical_query_params, "&")

    local canonical_headers_str = "host:" .. host .. "\n"
    local signed_headers = "host"

    local canonical_request = table.concat({
        method,
        uri_encode(path, false),
        canonical_query,
        canonical_headers_str,
        signed_headers,
        "UNSIGNED-PAYLOAD",
    }, "\n")

    local credential_scope = date_stamp .. "/" .. region .. "/" .. service .. "/aws4_request"
    local string_to_sign = table.concat({
        "AWS4-HMAC-SHA256",
        amz_date,
        credential_scope,
        sha256_hex(canonical_request),
    }, "\n")

    local signing_key = derive_signing_key(secret_key, date_stamp, region, service)
    local signature   = hmac_sha256_hex(signing_key, string_to_sign)

    return scheme .. "://" .. host .. path .. "?" .. canonical_query .. "&X-Amz-Signature=" .. signature
end

-- Expose helpers for testing
_M.uri_encode = uri_encode
_M.sha256_hex = sha256_hex

return _M
