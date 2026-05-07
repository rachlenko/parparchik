-- metrics.lua — Prometheus metrics rendering.

local _M = {}

--- Render Prometheus text-format metrics from the file registry.
-- @param entries  array of file entry tables from registry:list_all()
-- @param duplicates  number of duplicate file keys
function _M.render(entries, duplicates)
    local lines = {}

    -- Volume files by bucket
    local bucket_counts = {}
    local week_count  = 0
    local month_count = 0
    local now = os.time()
    local week_ago  = now - (7  * 24 * 3600)
    local month_ago = now - (31 * 24 * 3600)

    for _, entry in ipairs(entries) do
        local bkt = entry.bucket or "unknown"
        bucket_counts[bkt] = (bucket_counts[bkt] or 0) + 1

        -- Parse ISO 8601 timestamp for upload window metrics
        local lm = entry.last_modified or ""
        if lm ~= "" then
            -- Try parsing "2026-05-05T10:00:00Z" or similar
            local y, mo, d, h, mi, s = lm:match("(%d+)-(%d+)-(%d+)T(%d+):(%d+):(%d+)")
            if y then
                local t = os.time({
                    year = tonumber(y), month = tonumber(mo), day = tonumber(d),
                    hour = tonumber(h), min = tonumber(mi), sec = tonumber(s),
                })
                if t and t >= week_ago and t <= now then
                    week_count = week_count + 1
                end
                if t and t >= month_ago and t <= now then
                    month_count = month_count + 1
                end
            end
        end
    end

    -- parparchik_volume_files
    lines[#lines + 1] = "# HELP parparchik_volume_files Current number of known files by bucket."
    lines[#lines + 1] = "# TYPE parparchik_volume_files gauge"
    -- Sort bucket names for deterministic output
    local sorted_buckets = {}
    for bkt in pairs(bucket_counts) do
        sorted_buckets[#sorted_buckets + 1] = bkt
    end
    table.sort(sorted_buckets)
    for _, bkt in ipairs(sorted_buckets) do
        lines[#lines + 1] = string.format('parparchik_volume_files{volume="%s"} %d', bkt, bucket_counts[bkt])
    end

    -- parparchik_duplicate_files
    lines[#lines + 1] = "# HELP parparchik_duplicate_files Number of file keys that exist in more than one S3 bucket."
    lines[#lines + 1] = "# TYPE parparchik_duplicate_files gauge"
    lines[#lines + 1] = string.format("parparchik_duplicate_files %d", duplicates or 0)

    -- parparchik_uploads_per_week
    lines[#lines + 1] = "# HELP parparchik_uploads_per_week Known uploaded file versions modified during the last 7 days."
    lines[#lines + 1] = "# TYPE parparchik_uploads_per_week gauge"
    lines[#lines + 1] = string.format("parparchik_uploads_per_week %d", week_count)

    -- parparchik_uploads_per_month
    lines[#lines + 1] = "# HELP parparchik_uploads_per_month Known uploaded file versions modified during the last 31 days."
    lines[#lines + 1] = "# TYPE parparchik_uploads_per_month gauge"
    lines[#lines + 1] = string.format("parparchik_uploads_per_month %d", month_count)

    return table.concat(lines, "\n") .. "\n"
end

return _M
