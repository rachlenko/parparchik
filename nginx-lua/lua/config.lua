-- config.lua — Read parparchik configuration from environment variables.

local _M = {}

local function env(name, default)
    local v = os.getenv(name)
    if v and v ~= "" then return v end
    return default
end

function _M.load()
    local cfg = {
        aws_region         = env("AWS_REGION", "us-east-1"),
        s3_endpoint        = env("S3_ENDPOINT", ""),
        s3_external_endpoint = env("S3_EXTERNAL_ENDPOINT", ""),
        aws_access_key_id  = env("AWS_ACCESS_KEY_ID", ""),
        aws_secret_access_key = env("AWS_SECRET_ACCESS_KEY", ""),
        host               = env("PARPARCHIK_HOST", "0.0.0.0"),
        port               = tonumber(env("PARPARCHIK_PORT", "8080")),
        buckets            = {},
    }

    local buckets_str = env("PARPARCHIK_BUCKETS", "")
    if buckets_str ~= "" then
        for token in buckets_str:gmatch("[^,]+") do
            local parts = {}
            for part in token:gmatch("[^:]+") do
                parts[#parts + 1] = part
            end
            if #parts >= 1 then
                local bucket = {
                    name         = parts[1],
                    manifest_key = parts[2] or ".parparchik/files.json",
                    is_public    = (parts[3] == "public"),
                }
                cfg.buckets[#cfg.buckets + 1] = bucket
            end
        end
    else
        local pub  = env("PARPARCHIK_PUBLIC_BUCKET", "")
        local priv = env("PARPARCHIK_PRIVATE_BUCKET", "")
        if pub == "" or priv == "" then
            error("Set PARPARCHIK_BUCKETS or both PARPARCHIK_PUBLIC_BUCKET and PARPARCHIK_PRIVATE_BUCKET")
        end
        local manifest = env("PARPARCHIK_REGISTRY_MANIFEST_KEY", ".parparchik/files.json")
        cfg.buckets[1] = { name = pub,  manifest_key = manifest, is_public = true  }
        cfg.buckets[2] = { name = priv, manifest_key = manifest, is_public = false }
    end

    if #cfg.buckets == 0 then
        error("At least one bucket must be configured")
    end

    return cfg
end

function _M.is_bucket_public(cfg, bucket_name)
    for _, b in ipairs(cfg.buckets) do
        if b.name == bucket_name then
            return b.is_public
        end
    end
    return false
end

function _M.bucket_type(cfg, bucket_name)
    if _M.is_bucket_public(cfg, bucket_name) then
        return "public"
    end
    return "private"
end

return _M
