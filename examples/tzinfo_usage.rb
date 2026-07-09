# frozen_string_literal: true
#
# Usage of TZInfo — the IANA timezone engine added by `require "tzinfo"`:
# resolving zones, computing offsets/abbreviations at an instant, DST math,
# UTC<->local conversion, and country lookup. Runs under go-embedded-ruby
# (rbgo); see examples/README.md.

require "tzinfo"

# Resolve a zone by its IANA identifier.
tz = TZInfo::Timezone.get("America/New_York")
p tz.identifier                       # => "America/New_York"

# Two instants (UTC seconds since the epoch) on either side of a DST switch.
summer = Time.at(1721044800)          # 2024-07-15 12:00 UTC
winter = Time.at(1705320000)          # 2024-01-15 12:00 UTC

# Abbreviation and DST flag depend on the instant queried.
p tz.abbreviation(summer)             # => "EDT"
p tz.abbreviation(winter)             # => "EST"
p tz.dst?(summer)                     # => true
p tz.dst?(winter)                     # => false

# The TimezonePeriod in force gives the full offset breakdown (seconds).
per = tz.period_for_utc(summer)
p per.utc_total_offset                # => -14400   (base + std)
p per.base_utc_offset                 # => -18000   (standard time)
p per.std_offset                      # => 3600     (DST saving)

# Convert a UTC instant to wall-clock local time.
p tz.utc_to_local(summer).class       # => Time

# Country lookup: name and the zones it contains.
us = TZInfo::Country.get("US")
p us.name                             # => "United States"
p us.zone_identifiers.length          # => 29

# Unknown identifiers raise a typed error.
begin
  TZInfo::Timezone.get("No/Where")
rescue => e
  p e.class                           # => TZInfo::InvalidTimezoneIdentifier
end
