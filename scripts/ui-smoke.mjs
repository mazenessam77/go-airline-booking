import { randomUUID } from "node:crypto";

const base = process.env.DEMO_BASE_URL || "http://127.0.0.1:8080";
let token = "";

function assert(value, message) {
  if (!value) throw new Error(message);
}
async function request(path, options = {}) {
  const headers = { ...options.headers };
  if (token) headers.Authorization = `Bearer ${token}`;
  if (options.body) headers["Content-Type"] = "application/json";
  const response = await fetch(base + path, { ...options, headers });
  const body =
    response.status === 204 ? null : await response.json().catch(() => null);
  return { status: response.status, headers: response.headers, body };
}
async function account() {
  const email = `smoke+${randomUUID()}@example.test`;
  const password = `${randomUUID()}-${randomUUID()}`;
  let result = await request("/v1/auth/register", {
    method: "POST",
    body: JSON.stringify({
      email,
      password,
      first_name: "Smoke",
      last_name: "Traveler",
    }),
  });
  assert(result.status === 202, "registration failed");
  result = await request("/v1/auth/login", {
    method: "POST",
    body: JSON.stringify({ email, password }),
  });
  assert(
    result.status === 200 &&
      result.body.access_token &&
      result.body.refresh_token,
    "login failed",
  );
  return result.body;
}
async function createBooking(fare, passengerCount) {
  const quote = await request("/v1/quotes", {
    method: "POST",
    body: JSON.stringify({
      items: [{ fare_offer_id: fare.id, passenger_count: passengerCount }],
    }),
  });
  assert(quote.status === 201, "quote creation failed");
  const key = randomUUID();
  const options = {
    method: "POST",
    headers: { "Idempotency-Key": key },
    body: JSON.stringify({ quote_id: quote.body.id }),
  };
  const first = await request("/v1/bookings", options);
  const replay = await request("/v1/bookings", options);
  assert(
    first.status === 201 &&
      replay.status === 201 &&
      first.body.id === replay.body.id,
    "booking idempotency failed",
  );
  return first.body;
}
async function addPassenger(booking, index) {
  const response = await request(`/v1/bookings/${booking.id}/passengers`, {
    method: "POST",
    headers: { "Idempotency-Key": randomUUID() },
    body: JSON.stringify({
      passenger_type: "ADULT",
      first_name: `Demo${index}`,
      last_name: "Traveler",
      date_of_birth: "1995-06-15",
    }),
  });
  assert(response.status === 200, "passenger creation failed");
  return response.body;
}

for (const [path, type] of [
  ["/", "text/html"],
  ["/assets/style.css", "text/css"],
  ["/assets/app.js", "text/javascript"],
]) {
  const response = await fetch(base + path);
  assert(
    response.ok && response.headers.get("content-type")?.startsWith(type),
    `asset ${path} failed`,
  );
}
const ready = await request("/readyz");
assert(ready.status === 200, "backend is not ready");
const date = new Date();
date.setUTCDate(date.getUTCDate() + 1);
const flights = await request(
  `/v1/flights?origin=CAI&destination=DXB&date=${date.toISOString().slice(0, 10)}&limit=20`,
);
assert(
  flights.status === 200 && flights.body.items.length === 2,
  "seeded flights are unavailable",
);
const flight = await request(
  `/v1/flight-instances/${flights.body.items[0].id}`,
);
const fare = flight.body.fares.find((item) => item.cabin === "ECONOMY");
assert(
  flight.status === 200 && fare && Number.isInteger(fare.price_minor),
  "trusted fares are unavailable",
);
const map = await request(
  `/v1/flight-instances/${flight.body.id}/seats?limit=100`,
);
assert(
  map.status === 200 && map.body.items.length === 72,
  "seat map is incomplete",
);
assert(
  map.body.items.filter((seat) => seat.availability === "UNAVAILABLE").length >=
    3,
  "blocked/booked fixtures are missing",
);

const owner = await account();
token = owner.access_token;
const booking = await createBooking(fare, 2);
const passengerOne = await addPassenger(booking, 1);
const passengerTwo = await addPassenger(booking, 2);
let details = await request(`/v1/bookings/${booking.id}`);
assert(
  details.status === 200 &&
    details.body.passenger_count === 2 &&
    details.body.segments[0].cabin === "ECONOMY",
  "booking details are incomplete",
);
const available = map.body.items
  .filter(
    (seat) => seat.cabin === "ECONOMY" && seat.availability === "AVAILABLE",
  )
  .slice(0, 2);
const holdBody = {
  booking_segment_id: details.body.segments[0].id,
  flight_instance_id: flight.body.id,
  seats: [
    { passenger_id: passengerOne.id, flight_seat_id: available[0].id },
    { passenger_id: passengerTwo.id, flight_seat_id: available[1].id },
  ],
};
const holdKey = randomUUID();
const holdOptions = {
  method: "POST",
  headers: { "Idempotency-Key": holdKey },
  body: JSON.stringify(holdBody),
};
const hold = await request(
  `/v1/bookings/${booking.id}/seat-holds`,
  holdOptions,
);
const holdReplay = await request(
  `/v1/bookings/${booking.id}/seat-holds`,
  holdOptions,
);
assert(
  hold.status === 200 &&
    holdReplay.status === 200 &&
    hold.body.expires_at === holdReplay.body.expires_at,
  "seat-hold replay changed the hold",
);
let assignments = await request(`/v1/bookings/${booking.id}/seats`);
assert(
  assignments.status === 200 && assignments.body.items.length === 2,
  "atomic multi-seat hold failed",
);
const release = await request(`/v1/bookings/${booking.id}/seat-holds`, {
  method: "DELETE",
  headers: { "Idempotency-Key": randomUUID() },
});
assert(release.status === 204, "seat release failed");
assignments = await request(`/v1/bookings/${booking.id}/seats`);
assert(assignments.body.items.length === 0, "released seats remain active");
const cancel = await request(`/v1/bookings/${booking.id}/cancel`, {
  method: "POST",
  headers: { "Idempotency-Key": randomUUID() },
});
assert(cancel.status === 204, "booking cancellation failed");
const mine = await request("/v1/bookings?limit=20");
assert(
  mine.status === 200 &&
    mine.body.items.some(
      (item) => item.id === booking.id && item.status === "CANCELLED",
    ),
  "booking list failed",
);

const other = await account();
token = other.access_token;
const hidden = await request(`/v1/bookings/${booking.id}`);
assert(hidden.status === 404, "object ownership failed");

process.stdout.write(
  "UI/API smoke passed: assets, search, fares, auth, quote, booking replay, passengers, seat hold replay, release, cancellation, listing, and BOLA.\n",
);
