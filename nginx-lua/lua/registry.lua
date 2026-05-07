-- registry.lua — In-memory file registry backed by ngx.shared.DICT.
--
-- Entries are stored as JSON blobs in the shared dict keyed by file key.
-- A secondary key "route:<route>" maps routes to file keys for reverse lookup.
-- A special key "__all_keys__" holds a JSON array of all registered keys
-- for iteration.

local cjson = require "cjson.safe"

local _M = {}
local mt = { __index = _M }

local DICT_NAME = "file_registry"

local function get_dict()
    return ngx.shared[DICT_NAME]
end

function _M.new(buckets)
    local self = {
        buckets = buckets or {},
    }
    return setmetatable(self, mt)
end

-- ---------------------------------------------------------------------------
-- Route helpers
-- ---------------------------------------------------------------------------

local function make_route(key, bucket_name, buckets)
    -- Use bucket type prefix (/public/ or /private/) like the Python reference
    for _, b in ipairs(buckets) do
        if b.name == bucket_name then
            if b.is_public then
                return "/public/" .. key
            else
                return "/private/" .. key
            end
        end
    end
    return "/" .. bucket_name .. "/" .. key
end

local function bucket_type_str(bucket_name, buckets)
    for _, b in ipairs(buckets) do
        if b.name == bucket_name then
            return b.is_public and "public" or "private"
        end
    end
    return "private"
end

-- ---------------------------------------------------------------------------
-- Internal key management
-- ---------------------------------------------------------------------------

local function load_keys(dict)
    local raw = dict:get("__all_keys__")
    if not raw then return {} end
    return cjson.decode(raw) or {}
end

local function save_keys(dict, keys)
    dict:set("__all_keys__", cjson.encode(keys))
end

local function add_key(dict, key)
    local keys = load_keys(dict)
    for _, k in ipairs(keys) do
        if k == key then return end
    end
    keys[#keys + 1] = key
    save_keys(dict, keys)
end

local function remove_key(dict, key)
    local keys = load_keys(dict)
    local new_keys = {}
    for _, k in ipairs(keys) do
        if k ~= key then
            new_keys[#new_keys + 1] = k
        end
    end
    save_keys(dict, new_keys)
end

-- ---------------------------------------------------------------------------
-- Public API
-- ---------------------------------------------------------------------------

function _M.register_file(self, key, bucket_name, size, last_modified)
    local dict = get_dict()

    -- Check priority: lower index = higher priority
    local existing_raw = dict:get("file:" .. key)
    if existing_raw then
        local existing = cjson.decode(existing_raw)
        if existing then
            local existing_pri = #self.buckets + 1
            local new_pri = #self.buckets + 1
            for i, b in ipairs(self.buckets) do
                if b.name == existing.bucket then existing_pri = i end
                if b.name == bucket_name then new_pri = i end
            end
            if existing_pri < new_pri then
                return -- existing has higher priority
            end
        end
    end

    local route = make_route(key, bucket_name, self.buckets)
    local entry = {
        key           = key,
        bucket        = bucket_name,
        bucket_type   = bucket_type_str(bucket_name, self.buckets),
        route         = route,
        size          = size or 0,
        last_modified = last_modified or "",
    }

    -- Remove old route index if bucket changed
    if existing_raw then
        local old = cjson.decode(existing_raw)
        if old and old.route then
            dict:delete("route:" .. old.route)
        end
    end

    dict:set("file:" .. key, cjson.encode(entry))
    dict:set("route:" .. route, key)
    add_key(dict, key)
end

function _M.move_to_bucket(self, key, bucket_name)
    local dict = get_dict()
    local raw = dict:get("file:" .. key)
    if not raw then return end
    local entry = cjson.decode(raw)
    if not entry then return end

    -- Remove old route index
    dict:delete("route:" .. entry.route)

    entry.bucket      = bucket_name
    entry.bucket_type = bucket_type_str(bucket_name, self.buckets)
    entry.route       = make_route(key, bucket_name, self.buckets)

    dict:set("file:" .. key, cjson.encode(entry))
    dict:set("route:" .. entry.route, key)
end

function _M.remove(self, key)
    local dict = get_dict()
    local raw = dict:get("file:" .. key)
    if raw then
        local entry = cjson.decode(raw)
        if entry and entry.route then
            dict:delete("route:" .. entry.route)
        end
    end
    dict:delete("file:" .. key)
    remove_key(dict, key)
end

function _M.lookup(self, key)
    local dict = get_dict()
    local raw = dict:get("file:" .. key)
    if not raw then return nil end
    return cjson.decode(raw)
end

function _M.lookup_by_route(self, route)
    local dict = get_dict()
    local key = dict:get("route:" .. route)
    if not key then return nil end
    return self:lookup(key)
end

function _M.list_all(self)
    local dict = get_dict()
    local keys = load_keys(dict)
    local result = {}
    for _, key in ipairs(keys) do
        local raw = dict:get("file:" .. key)
        if raw then
            local entry = cjson.decode(raw)
            if entry then
                result[#result + 1] = entry
            end
        end
    end
    -- Sort by key
    table.sort(result, function(a, b) return a.key < b.key end)
    return result
end

function _M.list_by_bucket(self, bucket_name)
    local all = self:list_all()
    local result = {}
    for _, entry in ipairs(all) do
        if entry.bucket == bucket_name then
            result[#result + 1] = entry
        end
    end
    return result
end

function _M.manifest_for_bucket(self, bucket_name)
    local files = self:list_by_bucket(bucket_name)
    return {
        version = 1,
        bucket  = bucket_name,
        files   = files,
    }
end

function _M.clear(self)
    local dict = get_dict()
    local keys = load_keys(dict)
    for _, key in ipairs(keys) do
        local raw = dict:get("file:" .. key)
        if raw then
            local entry = cjson.decode(raw)
            if entry and entry.route then
                dict:delete("route:" .. entry.route)
            end
        end
        dict:delete("file:" .. key)
    end
    save_keys(dict, {})
end

function _M.load_manifests(self, manifests)
    -- Load in reverse priority order so higher-priority buckets overwrite
    self:clear()

    -- Reverse iterate
    for i = #manifests, 1, -1 do
        local m = manifests[i]
        local bucket_name = m.bucket
        local content     = m.content

        if content and content ~= "" then
            local parsed = cjson.decode(content)
            if parsed then
                local files = parsed
                if type(parsed) == "table" and parsed.files then
                    files = parsed.files
                end
                if type(files) == "table" then
                    for _, item in ipairs(files) do
                        if type(item) == "table" and item.key and item.key ~= "" then
                            local bkt = item.bucket
                            if not bkt or bkt == "" then bkt = bucket_name end
                            self:register_file(item.key, bkt, item.size, item.last_modified)
                        end
                    end
                end
            else
                ngx.log(ngx.WARN, "Ignoring invalid manifest for bucket: ", bucket_name)
            end
        end
    end
end

function _M.count(self)
    local dict = get_dict()
    local keys = load_keys(dict)
    return #keys
end

return _M
