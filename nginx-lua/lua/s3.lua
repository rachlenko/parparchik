-- s3.lua — S3 client using resty.http + AWS SigV4.

local http    = require "resty.http"
local cjson   = require "cjson.safe"
local aws_sig = require "aws_sig"

local _M = {}
local mt = { __index = _M }

function _M.new(cfg)
    local self = {
        endpoint          = cfg.s3_endpoint or "",
        external_endpoint = cfg.s3_external_endpoint or "",
        region            = cfg.aws_region or "us-east-1",
        access_key        = cfg.aws_access_key_id or "",
        secret_key        = cfg.aws_secret_access_key or "",
    }
    if self.external_endpoint == "" then
        self.external_endpoint = self.endpoint
    end
    return setmetatable(self, mt)
end

-- ---------------------------------------------------------------------------
-- Internal helpers
-- ---------------------------------------------------------------------------

-- Build the base URL for S3 requests (path-style).
local function base_url(self)
    if self.endpoint ~= "" then
        return "http://" .. self.endpoint
    end
    return "https://s3." .. self.region .. ".amazonaws.com"
end

-- Make a signed S3 request.
-- Returns body (string), status (number), response headers.
local function s3_request(self, method, bucket, key, body, extra_headers, query)
    local path = "/" .. bucket
    if key and key ~= "" then
        path = path .. "/" .. key
    end

    local host = self.endpoint
    if host == "" then
        host = "s3." .. self.region .. ".amazonaws.com"
    end

    local headers = {
        ["Host"] = host,
    }
    if extra_headers then
        for k, v in pairs(extra_headers) do
            headers[k] = v
        end
    end

    local sign_headers = aws_sig.sign_request({
        method     = method,
        path       = path,
        query      = query or "",
        headers    = headers,
        body       = body or "",
        region     = self.region,
        service    = "s3",
        access_key = self.access_key,
        secret_key = self.secret_key,
    })

    for k, v in pairs(sign_headers) do
        headers[k] = v
    end

    local url = base_url(self) .. path
    if query and query ~= "" then
        url = url .. "?" .. query
    end

    local httpc = http.new()
    httpc:set_timeout(10000) -- 10 seconds

    local res, err = httpc:request_uri(url, {
        method  = method,
        headers = headers,
        body    = body,
    })

    if not res then
        return nil, 0, nil, err
    end

    return res.body, res.status, res.headers, nil
end

-- ---------------------------------------------------------------------------
-- Parse S3 XML responses (minimal parsers for ListObjectsV2 and errors)
-- ---------------------------------------------------------------------------

local function xml_text(xml, tag)
    return xml:match("<" .. tag .. ">(.-)</" .. tag .. ">")
end

local function xml_iter(xml, tag)
    local pos = 1
    return function()
        local s, e = xml:find("<" .. tag .. ">(.-)</" .. tag .. ">", pos)
        if not s then return nil end
        local content = xml:sub(s, e)
        pos = e + 1
        return content
    end
end

-- ---------------------------------------------------------------------------
-- Public API
-- ---------------------------------------------------------------------------

--- List all objects in a bucket. Returns array of {key, size, last_modified}.
function _M.list_objects(self, bucket)
    local result = {}
    local continuation_token = nil

    while true do
        local query = "list-type=2&max-keys=1000"
        if continuation_token then
            query = query .. "&continuation-token=" .. ngx.escape_uri(continuation_token)
        end

        local body, status, _, err = s3_request(self, "GET", bucket, "", nil, nil, query)
        if err or status ~= 200 then
            ngx.log(ngx.ERR, "S3 ListObjects failed for ", bucket, ": ",
                    err or ("HTTP " .. tostring(status) .. " " .. (body or "")))
            break
        end

        for content in xml_iter(body, "Contents") do
            local key  = xml_text(content, "Key")
            local size = tonumber(xml_text(content, "Size") or "0")
            local lm   = xml_text(content, "LastModified") or ""
            if key then
                result[#result + 1] = {
                    key           = key,
                    size          = size,
                    last_modified = lm,
                }
            end
        end

        local is_truncated = xml_text(body, "IsTruncated")
        if is_truncated == "true" then
            continuation_token = xml_text(body, "NextContinuationToken")
            if not continuation_token then break end
        else
            break
        end
    end

    return result
end

--- HEAD an object. Returns {key, size, last_modified} or nil.
function _M.head_object(self, bucket, key)
    local _, status, resp_headers, err = s3_request(self, "HEAD", bucket, key)
    if err or status ~= 200 then
        return nil
    end

    local size = tonumber(resp_headers["Content-Length"] or resp_headers["content-length"] or "0")
    local lm   = resp_headers["Last-Modified"] or resp_headers["last-modified"] or ""

    return {
        key           = key,
        size          = size,
        last_modified = lm,
    }
end

--- Check if an object exists.
function _M.object_exists(self, bucket, key)
    return self:head_object(bucket, key) ~= nil
end

--- GET an object's content. Returns string or nil, error.
function _M.get_object(self, bucket, key)
    local body, status, _, err = s3_request(self, "GET", bucket, key)
    if err or status ~= 200 then
        return nil, err or ("HTTP " .. tostring(status))
    end
    return body
end

--- Try to GET an object's content. Returns content, found (bool).
function _M.try_get_object(self, bucket, key)
    local body, status = s3_request(self, "GET", bucket, key)
    if status == 200 and body then
        return body, true
    end
    return "", false
end

--- PUT an object.
function _M.put_object(self, bucket, key, content, content_type)
    content_type = content_type or "application/octet-stream"
    local extra = {
        ["Content-Type"]   = content_type,
        ["Content-Length"]  = tostring(#content),
    }
    local _, status, _, err = s3_request(self, "PUT", bucket, key, content, extra)
    if err or (status ~= 200 and status ~= 204) then
        ngx.log(ngx.ERR, "S3 PutObject failed: ", err or ("HTTP " .. tostring(status)))
        return false
    end
    return true
end

--- Generate a public URL for an object.
function _M.public_url(self, bucket, key)
    local ep = self.external_endpoint
    if ep ~= "" then
        return "http://" .. ep .. "/" .. bucket .. "/" .. key
    end
    return "https://" .. bucket .. ".s3." .. self.region .. ".amazonaws.com/" .. key
end

--- Generate a presigned URL for an object.
function _M.presigned_url(self, bucket, key, expires)
    expires = expires or 3600
    local host = self.external_endpoint
    if host == "" then
        host = bucket .. ".s3." .. self.region .. ".amazonaws.com"
    end
    local path = "/" .. bucket .. "/" .. key
    local scheme = "http"
    if self.endpoint == "" then
        scheme = "https"
        path = "/" .. key
    end

    return aws_sig.presigned_url({
        host       = host,
        path       = path,
        region     = self.region,
        service    = "s3",
        access_key = self.access_key,
        secret_key = self.secret_key,
        expires    = expires,
        scheme     = scheme,
    })
end

return _M
