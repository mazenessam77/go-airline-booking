# Troubleshooting labs

Start at the [Production Troubleshooting Lab README](../README.md). It explains the incidents, system, and setup before the commands.

- [CPU and the failed HTTP benchmark](labs/cpu.md)
- [Booking / seat hold latency](labs/booking-latency.md)
- [Observability and PromQL](observability.md)
- [Investigation method](troubleshooting-method.md)
- [Local setup](running.md)

The third fault, `LAB_FAULTS=latency`, adds a randomized 1.5–2.5-second wait to successful `GET /v1/flight-instances/{id}` requests after flight and fare queries. It is documented in [running the lab](running.md#enable-and-clear-faults). The two full incident studies focus on CPU and booking latency.
