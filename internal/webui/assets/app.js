const $ = (id) => document.getElementById(id);
const escape = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const money = (minor, currency = "EGP") =>
  new Intl.NumberFormat("en", {
    style: "currency",
    currency,
    maximumFractionDigits: 0,
  }).format(minor / 100);
const time = (value) =>
  new Date(value).toLocaleTimeString("en-GB", {
    hour: "2-digit",
    minute: "2-digit",
    timeZone: "UTC",
  });
const day = (value) =>
  new Date(value).toLocaleDateString("en-GB", {
    day: "numeric",
    month: "short",
    timeZone: "UTC",
  });
const cities = {
  CAI: "Cairo",
  DXB: "Dubai",
  LHR: "London",
  JED: "Jeddah",
  ASW: "Aswan",
};
const state = {
  tokens: null,
  email: "",
  flights: [],
  flight: null,
  fare: null,
  quote: null,
  booking: null,
  seats: [],
  assignments: [],
  selected: new Map(),
  activePassenger: null,
  editPassenger: null,
  view: "flights",
  bookings: [],
  registering: false,
  lastHold: null,
};
const pending = new Map();
let refreshPromise = null;
let busy = false;

function notice(message, error = false) {
  $("notice").textContent = message;
  $("notice").className = `notice${error ? " error" : ""}`;
  $("notice").hidden = !message;
}
const errors = {
  unavailable:
    "That seat or fare is no longer available. Refresh and choose another.",
  invalid_input: "Please check the details and try again.",
  invalid_credentials: "The email or password is incorrect.",
  not_found: "This booking could not be found.",
  rate_limited: "Too many attempts. Please wait a minute before trying again.",
  temporary_conflict: "The booking is busy. Retry the same action in a moment.",
  idempotency_conflict:
    "This request changed while being retried. Refresh your booking before continuing.",
  payments_disabled: "Payments are not connected in this demo.",
};
async function api(path, { method = "GET", body, key, retry = true } = {}) {
  const headers = {};
  if (state.tokens)
    headers.Authorization = `Bearer ${state.tokens.access_token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (key) headers["Idempotency-Key"] = key;
  let response;
  try {
    response = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "omit",
    });
  } catch {
    throw new Error(
      "Connection interrupted. Retry the same action when the server is available.",
    );
  }
  if (
    response.status === 401 &&
    retry &&
    state.tokens &&
    !path.startsWith("/v1/auth/")
  ) {
    await refresh();
    return api(path, { method, body, key, retry: false });
  }
  const data =
    response.status === 204 ? null : await response.json().catch(() => null);
  if (!response.ok) {
    const code = data?.error?.code;
    const error = new Error(
      errors[code] ||
        (response.status === 401
          ? "Please sign in to continue."
          : response.status === 429
            ? errors.rate_limited
            : "The request could not be completed. Please try again."),
    );
    error.status = response.status;
    throw error;
  }
  return data;
}
async function mutate(path, method, body, explicitKey) {
  const signature = `${method} ${path} ${JSON.stringify(body)}`;
  const key = explicitKey || pending.get(signature) || crypto.randomUUID();
  pending.set(signature, key);
  try {
    const result = await api(path, { method, body, key });
    pending.delete(signature);
    return result;
  } catch (error) {
    if (error.status >= 400 && error.status < 500) pending.delete(signature);
    throw error;
  }
}
function session(tokens, email) {
  state.tokens = tokens;
  state.email = email;
  $("account-button").textContent = tokens ? "My account ↗" : "Sign in ↗";
  $("account-email").textContent = email;
  $("demo-login").textContent = tokens
    ? "Demo guide ↗"
    : "Try a demo account ↗";
}
function clearSession() {
  session(null, "");
  pending.clear();
  state.booking = null;
  state.quote = null;
  state.assignments = [];
  state.selected.clear();
  state.bookings = [];
  state.lastHold = null;
  $("booking-list").replaceChildren();
  renderTrip();
}
async function refresh() {
  if (!refreshPromise)
    refreshPromise = (async () => {
      try {
        const tokens = await api("/v1/auth/refresh", {
          method: "POST",
          body: { refresh_token: state.tokens.refresh_token },
          retry: false,
        });
        session(tokens, state.email);
      } catch (error) {
        clearSession();
        throw error;
      } finally {
        refreshPromise = null;
      }
    })();
  return refreshPromise;
}
async function run(button, task) {
  if (busy) return;
  busy = true;
  if (button) button.disabled = true;
  try {
    await task();
  } catch (error) {
    notice(error.message, true);
  } finally {
    busy = false;
    if (button?.isConnected) button.disabled = false;
  }
}
function requireSession() {
  if (state.tokens) return true;
  $("auth-dialog").showModal();
  return false;
}
function switchView(view) {
  state.view = view;
  $("flight-results").hidden = view !== "flights";
  $("booking-list").hidden = view !== "bookings";
  $("flights-tab").classList.toggle("active", view === "flights");
  $("bookings-tab").classList.toggle("active", view === "bookings");
  $("more-flights").hidden = true;
  $("more-bookings").hidden = true;
  $("results-eyebrow").textContent =
    view === "flights" ? "LET’S GO SOMEWHERE" : "YOUR NEXT ADVENTURES";
  $("results-heading").textContent =
    view === "flights" ? "Available flights" : "My bookings";
}
async function search(more = false) {
  if ($("origin").value === $("destination").value)
    throw new Error("Choose two different airports.");
  switchView("flights");
  const params = new URLSearchParams({
    origin: $("origin").value,
    destination: $("destination").value,
    date: $("departure-date").value,
    limit: "20",
  });
  if (more && state.flights.length) {
    const last = state.flights.at(-1);
    params.set("after_id", last.id);
    params.set("after_departure", last.departure);
  }
  if (!more)
    $("flight-results").innerHTML =
      '<div class="empty-state loading-label">Finding your next adventure…</div>';
  const result = await api(`/v1/flights?${params}`);
  state.flights = more ? [...state.flights, ...result.items] : result.items;
  $("results-caption").textContent =
    `${cities[$("origin").value]} to ${cities[$("destination").value]} · ${day($("departure-date").value)} · All times UTC`;
  $("more-flights").hidden = result.items.length < 20;
  renderFlights();
}
function renderFlights() {
  $("flight-results").innerHTML = state.flights.length
    ? state.flights
        .map((f) => {
          const duration = Math.round(
            (new Date(f.arrival) - new Date(f.departure)) / 60000,
          );
          return `<article class="flight-card ${state.flight?.id === f.id ? "selected" : ""}"><div class="flight-card-top"><span class="airline-label"><span class="airline-icon" aria-hidden="true">✈</span>Airline Demo <small>${escape(f.airline)} ${escape(f.number)}</small></span><span class="tag">${escape(f.status.toLowerCase())}</span></div><div class="flight-route"><div class="flight-time"><strong>${time(f.departure)}</strong><span class="airport">${escape(f.origin)}</span><small>${escape(cities[f.origin] || f.origin)}</small></div><div class="flight-line"><small>${Math.floor(duration / 60)}h ${duration % 60}m</small><div class="route-line">✈</div><small>Nonstop</small></div><div class="flight-time"><strong>${time(f.arrival)}</strong><span class="airport">${escape(f.destination)}</span><small>${escape(cities[f.destination] || f.destination)}</small></div><div class="flight-action"><span class="fare-teaser">Your journey, your fare</span><button type="button" class="button ${state.flight?.id === f.id ? "secondary" : "primary"}" data-action="choose" data-id="${escape(f.id)}">${state.flight?.id === f.id ? "Selected ✓" : "View fares →"}</button></div></div><div class="flight-foot"><span>Economy & Business</span><span>${day(f.departure)} · Times shown in UTC</span></div></article>`;
        })
        .join("")
    : '<div class="empty-state"><h3>No flights on this route yet.</h3><p>Try Cairo ↔ Dubai, London, Jeddah, or Aswan in the next seven days.</p></div>';
}
async function choose(id) {
  const flight = await api(`/v1/flight-instances/${id}`);
  state.flight = flight;
  state.fare =
    flight.fares.find((f) => f.cabin === "ECONOMY")?.id || flight.fares[0]?.id;
  state.booking = null;
  state.quote = null;
  state.lastHold = null;
  state.selected.clear();
  state.editPassenger = null;
  renderFlights();
  renderTrip();
  if (matchMedia("(max-width: 1000px)").matches)
    $("booking-panel").scrollIntoView({ behavior: "smooth", block: "start" });
}
function renderTrip() {
  const f = state.flight,
    b = state.booking;
  if (!f) return;
  let content = `<div class="trip-route">${escape(f.origin)} <span>→</span> ${escape(f.destination)}</div><p class="trip-subtitle">${day(f.departure)} · ${time(f.departure)} UTC · ${escape(f.airline)} ${escape(f.number)}</p>`;
  if (!b) {
    content += `<h3>Choose your fare</h3><div class="fare-options">${f.fares.map((fare) => `<label class="fare-option"><input type="radio" name="fare" value="${escape(fare.id)}" ${fare.id === state.fare ? "checked" : ""}><span><strong>${escape(fare.cabin.toLowerCase())}</strong><small>${fare.refundable ? "Refundable fare" : "Non-refundable"} · Tax included</small></span><span class="fare-price">${money(fare.price_minor + fare.taxes_minor, fare.currency)}</span></label>`).join("")}</div>`;
    const fare = f.fares.find((v) => v.id === state.fare);
    if (fare)
      content += `<div class="total-row"><span>Estimate · ${$("traveler-count").value} traveler(s)</span><strong>${money((fare.price_minor + fare.taxes_minor) * Number($("traveler-count").value), fare.currency)}</strong></div><p class="tiny muted">Final price is verified by the server when you create your booking.</p><button class="button primary full" data-action="create" type="button">Continue with this flight →</button>`;
    else
      content +=
        '<p class="info-box">No active fares are available for this flight.</p>';
  } else {
    content += `<div class="booking-reference"><span>Booking <strong>${escape(b.pnr)}</strong></span><span class="status ${escape(b.status.toLowerCase())}">${escape(b.status.replaceAll("_", " "))}</span></div><div class="total-row"><span>Booking total · ${b.passenger_count} traveler(s)</span><strong>${money(b.total_minor, b.currency)}</strong></div>`;
    content += `<div class="passenger-list">${b.passengers.map((p) => `<div class="passenger-item"><span>${escape(p.first_name)} ${escape(p.last_name)}<small>${escape(p.passenger_type.toLowerCase())}</small></span>${b.status === "DRAFT" ? `<button type="button" class="text-button" data-action="edit-passenger" data-id="${escape(p.id)}">Edit</button>` : ""}</div>`).join("")}</div>`;
    if (
      b.status === "DRAFT" &&
      (b.passengers.length < b.passenger_count || state.editPassenger)
    ) {
      const p = b.passengers.find((p) => p.id === state.editPassenger);
      content += `<h3>${p ? "Edit traveler" : `Traveler ${b.passengers.length + 1} of ${b.passenger_count}`}</h3><form id="passenger-form" class="passenger-form"><div class="two-columns"><label>First name<input name="first_name" required maxlength="100" value="${escape(p?.first_name || "")}" placeholder="Alex"></label><label>Last name<input name="last_name" required maxlength="100" value="${escape(p?.last_name || "")}" placeholder="Morgan"></label></div><div class="two-columns"><label>Traveler type<select name="passenger_type">${["ADULT", "CHILD", "INFANT"].map((type) => `<option ${p?.passenger_type === type ? "selected" : ""}>${type}</option>`).join("")}</select></label><label>Date of birth<input name="date_of_birth" type="date" required max="${new Date().toISOString().slice(0, 10)}" value="${escape(p?.date_of_birth || "")}"></label></div><button type="submit" class="button primary full">${p ? "Save traveler" : "Add traveler"}</button><button type="button" class="text-button full" data-action="fill-passenger">Fill fictional details</button></form>`;
    }
    if (
      ["DRAFT", "HELD"].includes(b.status) &&
      b.passengers.length === b.passenger_count &&
      !state.editPassenger
    )
      content += seatMap();
    if (b.status === "HELD")
      content += `<div class="hold-banner"><strong>Your seats are held ✓</strong><span id="hold-countdown"></span></div><div class="trip-controls"><button type="button" class="button secondary" data-action="release">Release seats</button>${state.lastHold ? '<button type="button" class="button secondary" data-action="repeat">Repeat hold</button>' : ""}</div><button class="button secondary full" disabled>Payments not connected</button><p class="tiny muted">This is a seat hold, not a confirmed ticket. No payment will be taken.</p>`;
    if (["DRAFT", "HELD"].includes(b.status))
      content +=
        '<button type="button" class="text-button full" data-action="cancel">Cancel this booking</button>';
    if (["CANCELLED", "EXPIRED"].includes(b.status))
      content +=
        '<div class="info-box">This booking is closed. Choose a flight to start a new adventure.</div>';
    content +=
      '<button type="button" class="text-button full" data-action="reload">Refresh booking & seats</button>';
  }
  $("trip-content").innerHTML = `<div class="trip-body">${content}</div>`;
  countdown();
}
function seatMap() {
  const b = state.booking;
  if (b.segments.length !== 1)
    return '<div class="info-box">This simple demo supports single-flight seat selection only.</div>';
  if (
    !state.activePassenger ||
    !b.passengers.some((p) => p.id === state.activePassenger)
  )
    state.activePassenger = b.passengers[0]?.id;
  const rows = new Map();
  for (const seat of state.seats.filter(
    (s) => s.cabin === b.segments[0].cabin,
  )) {
    const match = seat.number.match(/^(\d+)([A-Z])$/);
    if (!match) continue;
    if (!rows.has(Number(match[1]))) rows.set(Number(match[1]), []);
    rows.get(Number(match[1])).push(seat);
  }
  return `<div class="seat-selector"><h3>Pick your favorite seat</h3><p class="tiny muted">${escape(b.segments[0].cabin.toLowerCase())} · Choose a traveler, then a seat.</p><div class="seat-travelers">${b.passengers.map((p) => `<button type="button" class="traveler-chip ${state.activePassenger === p.id ? "active" : ""}" data-action="traveler" data-id="${escape(p.id)}">${escape(p.first_name)} <b>${escape(state.seats.find((s) => s.id === state.selected.get(p.id))?.number || "—")}</b></button>`).join("")}</div><div class="seat-map" aria-label="Seat map">${[
    ...rows,
  ]
    .sort((a, b) => a[0] - b[0])
    .map(
      ([row, seats]) =>
        `<div class="seat-row"><span class="row-number">${row}</span>${seats
          .sort((a, b) => a.number.localeCompare(b.number))
          .map((s, i) => {
            const selected = [...state.selected.values()].includes(s.id);
            const unavailable =
              s.availability !== "AVAILABLE" || b.status !== "DRAFT";
            return `${i === 3 ? '<span aria-hidden="true"></span>' : ""}<button type="button" class="seat ${selected ? "selected" : ""}" data-action="seat" data-id="${escape(s.id)}" aria-label="Seat ${escape(s.number)}${selected ? ", selected" : unavailable ? ", unavailable" : ", available"}" aria-pressed="${selected}" ${unavailable ? "disabled" : ""}>${escape(s.number)}</button>`;
          })
          .join("")}</div>`,
    )
    .join(
      "",
    )}</div><div class="seat-legend"><span>□ Available</span><span>■ Selected</span><span>▧ Unavailable</span></div>${b.status === "DRAFT" ? `<button class="button primary full" data-action="hold" type="button" ${state.selected.size !== b.passenger_count ? "disabled" : ""}>Hold ${b.passenger_count === 1 ? "my seat" : "our seats"} for 15 minutes</button>` : ""}</div>`;
}
function countdown() {
  if (!$("hold-countdown")) return;
  const seconds = Math.max(
    0,
    Math.ceil((new Date(state.booking.hold_expires_at) - Date.now()) / 1000),
  );
  $("hold-countdown").textContent = seconds
    ? `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")} remaining · expires ${time(state.booking.hold_expires_at)} UTC`
    : "Hold expired. Refresh to see the latest status.";
}
async function loadBooking(id) {
  const booking = await api(`/v1/bookings/${id}`);
  const flight = booking.segments[0]
    ? await api(
        `/v1/flight-instances/${booking.segments[0].flight_instance_id}`,
      )
    : null;
  let seats = [],
    cursor = "";
  if (flight)
    for (let page = 0; page < 10; page++) {
      const result = await api(
        `/v1/flight-instances/${flight.id}/seats?limit=100${cursor ? `&after_id=${cursor}` : ""}`,
      );
      seats.push(...result.items);
      if (result.items.length < 100) break;
      cursor = result.items.at(-1).id;
    }
  const assignments = await api(`/v1/bookings/${id}/seats`);
  state.booking = booking;
  state.flight = flight;
  state.seats = seats;
  state.assignments = assignments.items;
  state.selected = new Map(
    assignments.items
      .filter((a) => ["HELD", "CONFIRMED"].includes(a.status))
      .map((a) => [a.passenger_id, a.seat_id]),
  );
  state.editPassenger = null;
  renderTrip();
  renderFlights();
}
async function listBookings(more = false) {
  if (!requireSession()) return;
  const after =
    more && state.bookings.length
      ? `&after_id=${state.bookings.at(-1).id}`
      : "";
  const result = await api(`/v1/bookings?limit=20${after}`);
  state.bookings = more ? [...state.bookings, ...result.items] : result.items;
  switchView("bookings");
  $("results-caption").textContent =
    "Only bookings belonging to your account appear here.";
  $("booking-list").innerHTML = state.bookings.length
    ? state.bookings
        .map(
          (b) =>
            `<article class="booking-card"><div><span class="eyebrow small-eyebrow">BOOKING REFERENCE</span><h3>${escape(b.pnr)}</h3><span class="status ${escape(b.status.toLowerCase())}">${escape(b.status)}</span></div><div><strong>${money(b.total_minor, b.currency)}</strong><button type="button" class="button secondary" data-action="open-booking" data-id="${escape(b.id)}">Open booking →</button></div></article>`,
        )
        .join("")
    : '<div class="empty-state"><h3>Your next adventure is waiting.</h3><p>Choose a flight to create your first booking.</p></div>';
  $("more-bookings").hidden = result.items.length < 20;
}
const actions = {
  choose: (button) => choose(button.dataset.id),
  create: async () => {
    if (!requireSession()) return;
    const signature = `${state.fare}:${$("traveler-count").value}`;
    if (!state.quote || state.quote.signature !== signature)
      state.quote = {
        ...(await api("/v1/quotes", {
          method: "POST",
          body: {
            items: [
              {
                fare_offer_id: state.fare,
                passenger_count: Number($("traveler-count").value),
              },
            ],
          },
        })),
        signature,
      };
    const booking = await mutate("/v1/bookings", "POST", {
      quote_id: state.quote.id,
    });
    await loadBooking(booking.id);
    notice("Your booking is ready. Add your travelers to choose seats.");
  },
  "fill-passenger": () => {
    const form = $("passenger-form");
    const names = ["Alex", "Jamie", "Taylor", "Robin"];
    form.elements.first_name.value =
      names[state.booking.passengers.length % names.length];
    form.elements.last_name.value = "Morgan";
    form.elements.date_of_birth.value = "1995-06-15";
    form.elements.passenger_type.value = "ADULT";
  },
  "edit-passenger": (button) => {
    state.editPassenger = button.dataset.id;
    renderTrip();
  },
  traveler: (button) => {
    state.activePassenger = button.dataset.id;
    renderTrip();
  },
  seat: (button) => {
    const id = button.dataset.id;
    if (state.selected.get(state.activePassenger) === id)
      state.selected.delete(state.activePassenger);
    else {
      if ([...state.selected.values()].includes(id))
        throw new Error("This seat is already selected for another traveler.");
      state.selected.set(state.activePassenger, id);
      const next = state.booking.passengers.find(
        (p) => !state.selected.has(p.id),
      );
      if (next) state.activePassenger = next.id;
    }
    renderTrip();
  },
  hold: async () => {
    const b = state.booking,
      segment = b.segments[0];
    const body = {
      booking_segment_id: segment.id,
      flight_instance_id: segment.flight_instance_id,
      seats: [...state.selected]
        .map(([passenger_id, flight_seat_id]) => ({
          passenger_id,
          flight_seat_id,
        }))
        .sort((a, b) => a.passenger_id.localeCompare(b.passenger_id)),
    };
    const path = `/v1/bookings/${b.id}/seat-holds`;
    if (JSON.stringify(state.lastHold?.body) !== JSON.stringify(body))
      state.lastHold = { path, body, key: crypto.randomUUID() };
    await mutate(path, "POST", body, state.lastHold.key);
    await loadBooking(b.id);
    notice("Seats held successfully. Your 15-minute countdown has started.");
  },
  repeat: async () => {
    const { path, body, key } = state.lastHold;
    await mutate(path, "POST", body, key);
    await loadBooking(state.booking.id);
    notice(
      "Same request replayed safely. No duplicate hold and no extra time added.",
    );
  },
  release: async () => {
    const id = state.booking.id;
    await mutate(`/v1/bookings/${id}/seat-holds`, "DELETE");
    state.lastHold = null;
    await loadBooking(id);
    notice("Seats released. You can choose again.");
  },
  cancel: async () => {
    if (!confirm("Cancel this unpaid booking and release its seats?")) return;
    const id = state.booking.id;
    await mutate(`/v1/bookings/${id}/cancel`, "POST");
    state.lastHold = null;
    await loadBooking(id);
    notice("Booking cancelled.");
    if (state.view === "bookings") await listBookings();
  },
  reload: () => loadBooking(state.booking.id),
  "open-booking": async (button) => {
    state.lastHold = null;
    await loadBooking(button.dataset.id);
    $("booking-panel").scrollIntoView({ behavior: "smooth", block: "start" });
  },
};
document.addEventListener("click", (event) => {
  const close = event.target.closest("[data-close]");
  if (close) $(close.dataset.close).close();
  const button = event.target.closest("button[data-action]");
  if (button && actions[button.dataset.action])
    run(button, () => actions[button.dataset.action](button));
});
document.addEventListener("change", (event) => {
  if (event.target.name === "fare") {
    state.fare = event.target.value;
    state.quote = null;
    renderTrip();
  }
});
document.addEventListener("submit", (event) => {
  if (event.target.id !== "passenger-form") return;
  event.preventDefault();
  const form = event.target;
  run(form.querySelector("[type=submit]"), async () => {
    const id = state.booking.id,
      passenger = state.editPassenger;
    await mutate(
      `/v1/bookings/${id}/passengers${passenger ? `/${passenger}` : ""}`,
      passenger ? "PATCH" : "POST",
      Object.fromEntries(new FormData(form)),
    );
    await loadBooking(id);
    notice("Traveler details saved.");
  });
});
$("search-form").addEventListener("submit", (event) => {
  event.preventDefault();
  run(event.submitter, () => search());
});
$("swap-route").onclick = () => {
  const origin = $("origin").value;
  $("origin").value = $("destination").value;
  $("destination").value = origin;
};
$("traveler-count").onchange = () => {
  if (!state.booking) {
    state.quote = null;
    renderTrip();
  }
};
$("more-flights").onclick = (e) => run(e.currentTarget, () => search(true));
$("more-bookings").onclick = (e) =>
  run(e.currentTarget, () => listBookings(true));
$("flights-tab").onclick = () => {
  switchView("flights");
  renderFlights();
};
$("bookings-tab").onclick = (e) => run(e.currentTarget, () => listBookings());
$("guide-button").onclick = () => $("guide-dialog").showModal();
$("account-button").onclick = () =>
  $(state.tokens ? "account-dialog" : "auth-dialog").showModal();
$("auth-toggle").onclick = () => {
  state.registering = !state.registering;
  $("register-names").hidden = !state.registering;
  const form = $("auth-form");
  form.elements.first_name.required = state.registering;
  form.elements.last_name.required = state.registering;
  form.elements.password.minLength = state.registering ? 12 : 1;
  form.elements.password.autocomplete = state.registering
    ? "new-password"
    : "current-password";
  $("auth-title").textContent = state.registering
    ? "Adventure starts here."
    : "Good to see you.";
  $("auth-description").textContent = state.registering
    ? "Use a password of at least 12 characters."
    : "Sign in to save your next adventure.";
  $("auth-submit").textContent = state.registering
    ? "Create account"
    : "Sign in";
  $("auth-toggle").textContent = state.registering
    ? "Already registered? Sign in"
    : "New here? Create an account";
  $("auth-error").hidden = true;
};
$("auth-form").onsubmit = (event) => {
  event.preventDefault();
  const form = event.target;
  run(event.submitter, async () => {
    const data = Object.fromEntries(new FormData(form));
    try {
      if (state.registering)
        await api("/v1/auth/register", { method: "POST", body: data });
      const tokens = await api("/v1/auth/login", {
        method: "POST",
        body: { email: data.email, password: data.password },
      });
      clearSession();
      session(tokens, data.email);
      form.reset();
      $("auth-dialog").close();
      notice("You’re signed in. Choose your flight to continue.");
      renderTrip();
    } catch (error) {
      $("auth-error").textContent = error.message;
      $("auth-error").hidden = false;
    }
  });
};
$("demo-login").onclick = (event) =>
  run(event.currentTarget, async () => {
    if (state.tokens) {
      $("guide-dialog").showModal();
      return;
    }
    const email = `demo+${crypto.randomUUID()}@example.test`,
      password = `${crypto.randomUUID()}-${crypto.randomUUID()}`;
    await api("/v1/auth/register", {
      method: "POST",
      body: { email, password, first_name: "Demo", last_name: "Traveler" },
    });
    session(
      await api("/v1/auth/login", {
        method: "POST",
        body: { email, password },
      }),
      email,
    );
    notice(
      "Your fresh demo account is ready. Use fictional passenger details and explore.",
    );
  });
$("refresh-session").onclick = (event) =>
  run(event.currentTarget, async () => {
    await refresh();
    $("account-dialog").close();
    notice("Session refreshed securely.");
  });
for (const id of ["logout", "logout-all"])
  $(id).onclick = (event) =>
    run(event.currentTarget, async () => {
      await api(`/v1/auth/${id}`, { method: "POST" });
      clearSession();
      $("account-dialog").close();
      switchView("flights");
      renderFlights();
      notice("You’re signed out.");
    });
const tomorrow = new Date();
tomorrow.setUTCDate(tomorrow.getUTCDate() + 1);
$("departure-date").value = tomorrow.toISOString().slice(0, 10);
$("departure-date").min = new Date().toISOString().slice(0, 10);
setInterval(countdown, 1000);
api("/readyz")
  .then(() => {
    $("connection").replaceChildren();
    const dot = document.createElement("i");
    $("connection").append(dot, "Live backend");
  })
  .catch(() => {
    $("connection").textContent = "Backend offline";
  });
run(null, () => search());
