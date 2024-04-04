panapi.Log("Loaded CO2 optimization script")

-- global variable to track "time"
tick = 0

-- global table to keep function call statistics
calls = {
   cur = {},
   old = {}
}

-- map remote address to path fingerprints
raddr2fps = {}

-- map path fingerprint to path
fp2path = {}

-- map path fingerprint to tick ("time") when path was last used
fp2last = {}

-- map path fingerprint to observed RTT
fp2rtt = {}

-- map path fingerprint to minimal observed RTT
fp2minrtt = {}

-- map path fingerprint to observed bandwidth
fp2bw = {}

-- map path fingerprint to used ticks
fp2ticks = {}

-- map path fingerprint to path ID (number)
fp2id = {}

-- map path fingerprint to number of local addresses
fp2laddrs = {}

-- map local address to preferences table
laddr2prefs = {}

-- map local address to path fingerprint
laddr2fp = {}

-- map local address to path switch microsecond
laddr2switchtime = {}

-- map local address to transferred bytes on the current path
laddr2bytes_on_path = {}

-- map path fingerprint to path lenght
fp2length = {}

-- which relative CO2 penalty is associated with which IA
ia2co2 = {}
-- which comment is associated with which IA
ia2comment = {}

-- which accumulated CO2 penalty is associated with which path fingerprint
fp2co2 = {}

-- sorted list of path fingerprints according to last path selection
lastfps = {}

-- which strategy to pick first? (has to be a field method in the "strategies" table below)
settings = {
   strategy = "shortestPath"
}

-- helper function to calculate cumulated CO2 penalty for path fingerprint
function calcfp2co2(fp)
   local result = 0
   for i, asentry in ipairs(fp2path[fp].Metadata.Interfaces) do
      result = result + (ia2co2[asentry.IA] or 0)
   end
   fp2co2[fp] = result
   return result
end

-- table with different functions to rank path fingerprints according to selection strategy
strategies = {
   -- return sorted 
   shortestPath = function(raddr)
      local fingerprints = {}
      for _, fp in ipairs(raddr2fps[raddr]) do
         table.insert(fingerprints, fp)
      end
      table.sort(fingerprints, function(fp_a, fp_b)
         return (fp2length[fp_a] or 0) < (fp2length[fp_b] or 0)
      end)
      return fingerprints
   end,
   lowestCO2 = function(raddr)
      local fingerprints = {}
      for _, fp in ipairs(raddr2fps[raddr]) do
         table.insert(fingerprints, fp)
      end
      table.sort(fingerprints, function(fp_a, fp_b)
         return (fp2co2[fp_a] or 0) < (fp2co2[fp_b] or 0)
      end)
      return fingerprints
   end,
   balancedCO2 = function(raddr)
      local fingerprints = {}
      for _, fp in ipairs(raddr2fps[raddr]) do
         table.insert(fingerprints, fp)
      end
      table.sort(fingerprints, function(fp_a, fp_b)
         -- "balancing strategy" taking into account path lengths. Currently hand-tuned for demo with hand-picked CO2 emission values between CMU and MD
         return (fp2co2[fp_a] or 0) + 6 * fp2length[fp_a] < (fp2co2[fp_b] or 0) + 6 * fp2length[fp_b]
      end)
      return fingerprints
   end
}

-- gets called every second or so
function panapi.Periodic(seconds)
   tick = tick + 1
end

-- main entry point to manipulate script state via JSON
-- Lua tables that get returned here are turned into JSON response
function panapi.JSON(args)
   for _, entry in ipairs(args) do
      local ia = entry["IA"]
      ia2co2[ia] = entry["GramsPerMBIT"]
      ia2comment[ia] = entry["Comment"]
   end
   for fp, _ in pairs(fp2path) do
      local co2 = calcfp2co2(fp)
      fp2co2[fp] = co2
   end
   if strategies[args.strategy] ~= nil then
      panapi.Log("setting strategy to", args.strategy)
      settings.strategy = args.strategy
   end
   --[[local out = {}
    for _, fp in ipairs(lastfps) do
        table.insert(out, fp2info(fp))
    end
    return out]]
   return {settings, fp2info(lastfps[1])}
end

-- helper function to extract information from a path fingerprint
function fp2info(fp)
   local entry = {}
   entry.Fingerprint = fp
   entry.CO2 = fp2co2[fp]
   entry.ASPath = ""

   -- usually the following is sufficient
   -- local ifaces = fp2path[fp].Metadata.Interfaces
   -- but we need to prevent runtime errors in case of fp2path[fp] == nil or fp2path[fp].Metadata = nil, etc
   local ifaces = (((fp2path[fp] or {}).Metadata or {}).Interfaces or {})
   entry.Hops = #ifaces
   for i, iface in ipairs(ifaces) do
      if math.fmod(i, 2) == 1 or i == #ifaces then
         local spacer = " → "
         if i == #ifaces then
            spacer = ""
         end
         entry.ASPath = entry.ASPath .. tostring(ia2comment[iface.IA] or iface.IA) .. spacer
      end
   end
   return entry
end

-- gets called when a set of paths to addr is known
function panapi.Initialize(prefs, laddr, raddr, ps)
   panapi.Log("New connection [" .. laddr, "|", raddr .. "]")
   raddr2fps[raddr] = raddr2fps[raddr] or {}
   table.sort(ps, function(a, b)
      return a.Fingerprint < b.Fingerprint
   end)
   panapi.Log("Got", #ps, "paths")
   for i, path in ipairs(ps) do
      local fp = path.Fingerprint
      fp2length[fp] = #path.Metadata.Interfaces
      fp2path[fp] = path
      fp2id[fp] = i
      fp2laddrs[fp] = fp2laddrs[fp] or 0
      calcfp2co2(fp)
      --panapi.Log("Path", i, fp)
      local exists = false
      for i, fp2 in ipairs(raddr2fps[raddr]) do
         if fp == fp2 then
            exists = true
            break
         end
      end
      if exists == false then
         table.insert(raddr2fps[raddr], fp)
      end
   end
   --panapi.SetPreferences(prefs, laddr, raddr)
end

function panapi.SetPreferences(prefs, laddr, raddr)
   panapi.Log("Update Preferences [" .. laddr, "|", raddr .. "] Profile:", tprint(prefs))
   if prefs ~= nil then
      laddr2prefs[laddr] = laddr2prefs[laddr] or {}
      laddr2prefs[laddr] = prefs
   end
end

-- gets called for every packet
-- implementation needs to be efficient
function panapi.Path(laddr, raddr)
   if #raddr2fps[raddr] > 0 then
      local strategy = strategies[settings.strategy]
      lastfps = strategy(raddr)
      -- lastfps = strategies.balancedCO2(raddr)
      -- lastfps = strategies.shortest(raddr)
      -- lastfps = strategies.lowestCO2(raddr)
      return fp2path[lastfps[1]]
   end
end

-- gets called whenever a path disappears(?)
function panapi.PathDown(laddr, raddr, fp, pi)
   panapi.Log("PathDown called with", laddr, raddr, fp, pi)
   fp2path[fp] = nil
   fp2rtt[fp] = nil
   fp2last[fp] = nil
   fp2bw[fp] = nil
   fp2laddrs[fp] = 0

   for i, fp2 in ipairs(raddr2fps[raddr]) do
      if fp == fp2 then
         table.remove(raddr2fps[raddr], i)
      end
   end
   for laddr, fp2 in pairs(laddr2fp) do
      if fp == fp2 then
         laddr2fp[laddr] = nil
      end
   end

   --   shutdown = tick + 10
end

function panapi.Refresh(laddr, raddr, ps)
   panapi.Log("Refresh", raddr, ps)
   panapi.Initialize(nil, laddr, raddr, ps)
   --   shutdown = tick + 10
end

function panapi.Close(laddr, raddr)
   --panapi.Log("Close", laddr, raddr, laddr2fp[laddr])
   -- panapi.Log(tprint(fp2bw))
   -- panapi.Log(tprint(fp2laddrs))
   local fp = laddr2fp[laddr]
   laddr2fp[laddr] = nil
   if fp ~= nil then
      fp2laddrs[fp] = (fp2laddrs[fp] or 1) - 1
   end
   laddr2prefs[laddr] = nil
   -- shutdown = tick + 10
end

function stats.UpdatedMetrics(laddr, raddr, rttStats, cwnd, bytesInFlight, packetsInFlight)
   calls.cur.UpdatedMetrics = (calls.cur.UpdatedMetrics or 0) + 1
   local now = panapi.Now()
   local fp = laddr2fp[laddr]
   if fp == nil then
      return
   end
   if now - (laddr2switchtime[laddr] or 0) > 100000 then
      if rttStats.LatestRTT > 0 then
         fp2minrtt[fp] = math.min((fp2minrtt[fp] or rttStats.LatestRTT), rttStats.LatestRTT)
      end
   end
   --   fp2last[fp] = tick
   fp2rtt[fp] = rttStats.LatestRTT

   --   panapi.Log("\n", tprint(rttStats, 1))
end

function stats.SentPacket(laddr, raddr, size)
   calls.cur.SentPacket = (calls.cur.SentPacket or 0) + 1
   laddr2bytes_on_path[laddr] = (laddr2bytes_on_path[laddr] or 0) + size
end

function diffcallstats()
   local d = {}
   for k, v in pairs(calls.cur) do
      local old = calls.old[k]
      if v ~= old then
         d[k] = v - (old or 0)
      end
   end
   calls.old = copy(calls.cur)
   panapi.Log("function calls since last tick:\n" .. tprint(d, 2))
end

function abscallstats()
   local d = {}
   for k, v in pairs(calls.cur) do
      if v ~= 0 then
         d[k] = v
      end
   end
   panapi.Log("function calls:\n" .. tprint(d, 2))
end

function stats.TracerForConnection(id, p, odcid)
   calls.cur.TracerForConnection = (calls.cur.TracerForConnection or 0) + 1
   -- panapi.Log("id:", id, "perspective", p, "odcid", odcid)
end
function stats.StartedConnection(laddr, raddr, srcid, dstid)
   calls.cur.StartedConnection = (calls.cur.StartedConnection or 0) + 1

end
function stats.NegotiatedVersion(laddr, raddr)
   calls.cur.NegotiatedVersion = (calls.cur.NegotiatedVersion or 0) + 1

end
function stats.ClosedConnection(laddr, raddr)
   calls.cur.ClosedConnection = (calls.cur.ClosedConnection or 0) + 1

end
function stats.Close(laddr, raddr)
   calls.cur.Close = (calls.cur.Close or 0) + 1

end
function stats.SentTransportParameters(laddr, raddr)
   calls.cur.SentTransportParameters = (calls.cur.SentTransportParameters or 0) + 1

end
function stats.ReceivedTransportParameters(laddr, raddr)
   calls.cur.ReceivedTransportParameters = (calls.cur.ReceivedTransportParameters or 0) + 1

end
function stats.RestoredTransportParameters(laddr, raddr)
   calls.cur.RestoredTransportParameters = (calls.cur.RestoredTransportParameters or 0) + 1

end
function stats.ReceivedVersionNegotiationPacket(laddr, raddr)
   calls.cur.ReceivedVersionNegotiationPacket = (calls.cur.ReceivedVersionNegotiationPacket or 0) + 1

end
function stats.ReceivedRetry(laddr, raddr)
   calls.cur.ReceivedRetry = (calls.cur.ReceivedRetry or 0) + 1

end
function stats.ReceivedPacket(laddr, raddr)
   calls.cur.ReceivedPacket = (calls.cur.ReceivedPacket or 0) + 1

end
function stats.BufferedPacket(laddr, raddr)
   calls.cur.BufferedPacket = (calls.cur.BufferedPacket or 0) + 1

end
function stats.DroppedPacket(laddr, raddr)
   calls.cur.DroppedPacket = (calls.cur.DroppedPacket or 0) + 1

end
function stats.AcknowledgedPacket(laddr, raddr)
   calls.cur.AcknowledgedPacket = (calls.cur.AcknowledgedPacket or 0) + 1

end
function stats.LostPacket(laddr, raddr)
   calls.cur.LostPacket = (calls.cur.LostPacket or 0) + 1

end
function stats.UpdatedCongestionState(laddr, raddr)
   calls.cur.UpdatedCongestionState = (calls.cur.UpdatedCongestionState or 0) + 1

end
function stats.UpdatedPTOCount(laddr, raddr)
   calls.cur.UpdatedPTOCount = (calls.cur.UpdatedPTOCount or 0) + 1

end
function stats.UpdatedKeyFromTLS(laddr, raddr)
   calls.cur.UpdatedKeyFromTLS = (calls.cur.UpdatedKeyFromTLS or 0) + 1

end
function stats.UpdatedKey(laddr, raddr)
   calls.cur.UpdatedKey = (calls.cur.UpdatedKey or 0) + 1

end
function stats.DroppedEncryptionLevel(laddr, raddr)
   calls.cur.DroppedEncryptionLevel = (calls.cur.DroppedEncryptionLevel or 0) + 1

end
function stats.DroppedKey(laddr, raddr)
   calls.cur.DroppedKey = (calls.cur.DroppedKey or 0) + 1

end
function stats.SetLossTimer(laddr, raddr)
   calls.cur.SetLossTimer = (calls.cur.SetLossTimer or 0) + 1

end
function stats.LossTimerExpired(laddr, raddr)
   calls.cur.LossTimerExpired = (calls.cur.LossTimerExpired or 0) + 1

end
function stats.LossTimerCanceled(laddr, raddr)
   calls.cur.LossTimerCanceled = (calls.cur.LossTimerCanceled or 0) + 1

end
function stats.Debug(laddr, raddr)
   calls.cur.Debug = (calls.cur.Debug or 0) + 1
end

-- HELPER FUNCTIONS ---
-- 
-- Print contents of `tbl`, with indentation.
-- `indent` sets the initial level of indentation.
function tprint(tbl, indent, cutoff)
   if not indent then
      indent = 0
   end
   if not cutoff then
      cutoff = 3
   end
   if type(tbl) == "table" then
      local s = ""
      for k, v in pairs(tbl) do
         formatting = string.rep("  ", indent) .. tprint(k, indent) .. ": "
         s = s .. formatting
         if type(v) == "table" then
            if indent > cutoff then
               -- print(formatting)
               s = s .. "[cutoff]\n"
            else
               s = s .. "\n" .. tprint(v, indent + 1)
            end
         else
            s = s .. tprint(v) .. "\n"
         end
      end
      return s
   else
      return tostring(tbl)
   end
end

-- recursively perform a deep copy of a table
function copy(thing)
   if type(thing) == "table" then
      local r = {}
      for k, v in pairs(thing) do
         r[k] = copy(v)
      end
      return r
   else
      return thing
   end
end

-- return a new thing containing everything about thing1 that is different from thing2
function diff(thing1, thing2)
   if type(thing1) == "table" then
      local thing = {}
      if type(thing2) == "table" then
         local thing = {}
         for k, v in pairs(thing1) do
            thing[k] = diff(thing1[k], thing2[k])
         end
         return thing
      else
         return copy(thing1)
      end
   else
      if thing1 == thing2 then
         return nil
      else
         return thing1
      end
   end
end

