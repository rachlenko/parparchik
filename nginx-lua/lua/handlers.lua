-- handlers.lua — HTTP request handlers for parparchik Nginx+Lua.

local cjson    = require "cjson.safe"
local config   = require "config"
local s3_mod   = require "s3"
local registry = require "registry"
local metrics  = require "metrics"

local _M = {}

-- These are initialized in init_worker and stored in module-level vars
-- because ngx.shared.DICT is available across all phases.
local cfg
local s3
local reg
local duplicate_count = 0

-- Use shared dict for ready state so all workers see it
local function is_ready()
    local dict = ngx.shared.file_registry
    local val = dict:get("__ready__")
    return val == 1
end

local function set_ready(val)
    local dict = ngx.shared.file_registry
    dict:set("__ready__", val and 1 or 0)
end

-- ---------------------------------------------------------------------------
-- Initialization (called from init_worker_by_lua)
-- ---------------------------------------------------------------------------

function _M.init()
    cfg = config.load()
    s3  = s3_mod.new(cfg)
    reg = registry.new(cfg.buckets)

    -- Load manifests from S3
    ngx.log(ngx.INFO, "parparchik: loading registry manifests...")
    local manifests = {}
    local all_present = true

    for _, bucket in ipairs(cfg.buckets) do
        local content, found = s3:try_get_object(bucket.name, bucket.manifest_key)
        manifests[#manifests + 1] = {
            bucket  = bucket.name,
            content = content,
        }
        if not found then
            all_present = false
        end
    end

    reg:load_manifests(manifests)

    if not all_present then
        _M._backfill_registry()
    end

    _M._reconcile_registry()
    _M._persist_manifests()
    set_ready(true)
    ngx.log(ngx.INFO, "parparchik: ready (", reg:count(), " files)")
end

-- Lazy init for request handlers — ensures cfg/s3/reg are available
local function ensure_init()
    if not cfg then
        cfg = config.load()
        s3  = s3_mod.new(cfg)
        reg = registry.new(cfg.buckets)
    end
end

-- ---------------------------------------------------------------------------
-- Registry management
-- ---------------------------------------------------------------------------

function _M._backfill_registry()
    local seen = {}
    for _, bucket in ipairs(cfg.buckets) do
        local objects = s3:list_objects(bucket.name)
        for _, obj in ipairs(objects) do
            if obj.key ~= bucket.manifest_key and not seen[obj.key] then
                reg:register_file(obj.key, bucket.name, obj.size, obj.last_modified)
                seen[obj.key] = true
            end
        end
    end
end

function _M._reconcile_registry()
    local dup = 0
    local entries = reg:list_all()
    for _, entry in ipairs(entries) do
        local bucket_count = 0
        local registered = false
        for _, bucket in ipairs(cfg.buckets) do
            local obj = s3:head_object(bucket.name, entry.key)
            if obj then
                if not registered then
                    reg:register_file(entry.key, bucket.name, obj.size, obj.last_modified)
                    registered = true
                end
                bucket_count = bucket_count + 1
            end
        end
        if not registered then
            reg:remove(entry.key)
        end
        if bucket_count > 1 then
            dup = dup + 1
        end
    end
    duplicate_count = dup
end

function _M._persist_manifests()
    for _, bucket in ipairs(cfg.buckets) do
        local manifest = reg:manifest_for_bucket(bucket.name)
        local json_str = cjson.encode(manifest)
        if not s3:put_object(bucket.name, bucket.manifest_key, json_str, "application/json") then
            ngx.log(ngx.ERR, "Failed to write manifest to ", bucket.name)
        end
    end
end

local function sync_registry()
    -- Clear registry first so moved files aren't blocked by stale priority
    reg:clear()

    local seen = {}
    local dups = {}

    for _, bucket in ipairs(cfg.buckets) do
        local objects = s3:list_objects(bucket.name)
        for _, obj in ipairs(objects) do
            if obj.key ~= bucket.manifest_key then
                if not seen[obj.key] then
                    reg:register_file(obj.key, bucket.name, obj.size, obj.last_modified)
                else
                    dups[obj.key] = true
                end
                seen[obj.key] = true
            end
        end
    end

    local dup_count = 0
    for _ in pairs(dups) do dup_count = dup_count + 1 end
    duplicate_count = dup_count
    _M._persist_manifests()
end

local function resolve_missing_file(key)
    for _, bucket in ipairs(cfg.buckets) do
        local obj = s3:head_object(bucket.name, key)
        if obj then
            reg:register_file(key, bucket.name, obj.size, obj.last_modified)
            _M._persist_manifests()
            return reg:lookup(key)
        end
    end
    reg:remove(key)
    _M._persist_manifests()
    return nil
end

local function resolve_route(route)
    local entry = reg:lookup_by_route(route)
    if entry then
        if s3:object_exists(entry.bucket, entry.key) then
            return entry
        end
        -- File is gone from registered bucket; try to find it elsewhere
        local resolved = resolve_missing_file(entry.key)
        -- Only return if the resolved entry's route matches the requested route
        if resolved and resolved.route == route then
            return resolved
        end
        return nil
    end

    -- Extract key from route: /<bucket-name>/<key>
    for _, bucket in ipairs(cfg.buckets) do
        local bprefix = "/" .. bucket.name .. "/"
        if route:sub(1, #bprefix) == bprefix then
            local resolved = resolve_missing_file(route:sub(#bprefix + 1))
            if resolved and resolved.route == route then
                return resolved
            end
            return nil
        end
    end

    return nil
end

-- ---------------------------------------------------------------------------
-- JSON response helper
-- ---------------------------------------------------------------------------

local function json_response(status, body)
    ngx.status = status
    ngx.header["Content-Type"] = "application/json"
    ngx.say(cjson.encode(body))
    ngx.exit(status)
end

-- ---------------------------------------------------------------------------
-- Route handlers
-- ---------------------------------------------------------------------------

function _M.handle_status()
    ensure_init()
    local bucket_list = {}
    for _, b in ipairs(cfg.buckets) do
        bucket_list[#bucket_list + 1] = {
            name         = b.name,
            manifest_key = b.manifest_key,
            public       = b.is_public,
        }
    end

    json_response(200, {
        status      = "ok",
        buckets     = bucket_list,
        region      = cfg.aws_region,
        ready       = is_ready(),
        file_count  = reg:count(),
        -- backward compat for e2e test
        public_bucket  = cfg.buckets[1] and cfg.buckets[1].name or "",
        private_bucket = cfg.buckets[2] and cfg.buckets[2].name or "",
    })
end

function _M.handle_readiness()
    ensure_init()
    if is_ready() then
        json_response(200, {
            status     = "ready",
            ready      = true,
            file_count = reg:count(),
        })
    else
        json_response(503, {
            status     = "not_ready",
            ready      = false,
            file_count = reg:count(),
        })
    end
end

function _M.handle_healthcheck()
    ensure_init()
    json_response(200, {
        status = "ok",
        ready  = is_ready(),
    })
end

function _M.handle_list()
    ensure_init()
    sync_registry()
    local entries = reg:list_all()
    json_response(200, {
        count = #entries,
        files = entries,
    })
end

function _M.handle_update()
    ensure_init()
    local filename = ngx.var.arg_filename
    if not filename or filename == "" then
        json_response(400, { error = "missing 'filename' query parameter" })
        return
    end

    local entry = reg:lookup(filename)
    if entry then
        -- Verify the file still exists at its registered location
        if not s3:object_exists(entry.bucket, entry.key) then
            entry = resolve_missing_file(filename)
        end
    else
        entry = resolve_missing_file(filename)
    end
    if not entry then
        json_response(404, { error = "file not found", filename = filename })
        return
    end

    json_response(200, { message = "file located", file = entry })
end

function _M.handle_relocate()
    ensure_init()
    local filename = ngx.var.arg_filename
    if not filename or filename == "" then
        json_response(400, { error = "missing 'filename' query parameter" })
        return
    end

    local target_bucket = nil
    local target_object = nil
    local found_count   = 0

    for _, bucket in ipairs(cfg.buckets) do
        local obj = s3:head_object(bucket.name, filename)
        if obj then
            target_bucket = bucket.name
            target_object = obj
            found_count = found_count + 1
        end
    end

    if found_count == 0 then
        reg:remove(filename)
        pcall(_M._persist_manifests)
        json_response(404, {
            status   = "fail",
            error    = "file not found in any bucket",
            filename = filename,
        })
        return
    end

    local previous = reg:lookup(filename)

    if previous and previous.bucket ~= target_bucket then
        reg:move_to_bucket(filename, target_bucket)
    end
    reg:register_file(filename, target_bucket, target_object.size, target_object.last_modified)

    local ok, err = pcall(_M._persist_manifests)
    if not ok then
        json_response(500, {
            status   = "fail",
            error    = "failed to persist manifests",
            filename = filename,
        })
        return
    end

    local entry = reg:lookup(filename)
    local resp = { status = "ok", file = entry }
    if previous and previous.bucket ~= target_bucket then
        resp.relocated_from = previous.bucket
        resp.relocated_to   = target_bucket
    end
    if found_count > 1 then
        resp.duplicate = true
        resp.note = "file exists in multiple buckets, last configured bucket takes precedence"
    end
    json_response(200, resp)
end

function _M.handle_download()
    ensure_init()
    local route = ngx.var.uri

    local entry = resolve_route(route)
    if not entry then
        json_response(404, { error = "file not found", route = route })
        return
    end

    local url
    if entry.bucket_type == "public" then
        url = s3:public_url(entry.bucket, entry.key)
    else
        url = s3:presigned_url(entry.bucket, entry.key, 3600)
    end

    ngx.redirect(url, 302)
end

function _M.handle_metrics()
    ensure_init()
    local entries = reg:list_all()
    local body = metrics.render(entries, duplicate_count)
    ngx.status = 200
    ngx.header["Content-Type"] = "text/plain; version=0.0.4"
    ngx.say(body)
    ngx.exit(200)
end

return _M
