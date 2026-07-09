# tzinfo examples

Runnable pure-Ruby usage of the `tzinfo` IANA timezone engine, verified under the [rbgo](https://github.com/go-embedded-ruby) interpreter.

```sh
rbgo examples/tzinfo_usage.rb
```

| File | Shows |
| --- | --- |
| `tzinfo_usage.rb` | Resolve a zone with `TZInfo::Timezone.get`; read the `abbreviation` and `dst?` at a given instant; break down a `period_for_utc` into `utc_total_offset` / `base_utc_offset` / `std_offset`; convert UTC to local with `utc_to_local`; look up a `TZInfo::Country` name and its `zone_identifiers`; and rescue `TZInfo::InvalidTimezoneIdentifier`. |
