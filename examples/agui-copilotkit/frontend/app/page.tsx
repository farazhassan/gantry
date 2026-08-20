"use client";

import { useCopilotAction } from "@copilotkit/react-core";
import { CopilotChat } from "@copilotkit/react-ui";

// Registers a *frontend* action: its definition (name, description,
// parameters) is sent to the Gantry server per-request in
// RunAgentInput.tools, and the handler runs entirely in this browser --
// nothing about "get_location" is ever known to the Go server. This is
// exactly what tool.DynamicClient advertises and suspends on: the model
// calls "get_location", the server has never heard of it, the run
// suspends, and CopilotKit resolves it here using the real Geolocation API
// -- something a Go server could never do on its own.
function LocationAction() {
  useCopilotAction({
    name: "get_location",
    description: "Returns the browser's current location.",
    parameters: [],
    handler: async () => {
      if (!("geolocation" in navigator)) {
        return { error: "Geolocation is not available in this browser." };
      }
      try {
        const pos = await new Promise<GeolocationPosition>(
          (resolve, reject) =>
            navigator.geolocation.getCurrentPosition(resolve, reject, {
              timeout: 10_000,
            }),
        );
        return {
          lat: pos.coords.latitude,
          lng: pos.coords.longitude,
          accuracyMeters: pos.coords.accuracy,
        };
      } catch (err) {
        const message =
          err instanceof GeolocationPositionError
            ? geolocationErrorMessage(err)
            : "Failed to read the browser's location.";
        return { error: message };
      }
    },
    render: ({ status }) => <span>Locating you... ({status})</span>,
  });
  return null;
}

function geolocationErrorMessage(err: GeolocationPositionError): string {
  switch (err.code) {
    case err.PERMISSION_DENIED:
      return "Location permission was denied by the user.";
    case err.POSITION_UNAVAILABLE:
      return "Position unavailable.";
    case err.TIMEOUT:
      return "Timed out while getting the current position.";
    default:
      return "Failed to read the browser's location.";
  }
}

export default function Home() {
  return (
    <main style={{ maxWidth: 720, margin: "0 auto", padding: "2rem" }}>
      <h1>Gantry + CopilotKit demo</h1>
      <p>
        Ask &ldquo;Where am I?&rdquo; below. The model calls{" "}
        <code>get_location</code>, a tool declared only by this page (via{" "}
        <code>useCopilotAction</code>) and never registered in the Go
        server&apos;s code -- the Gantry run suspends until this browser
        answers with the real <code>navigator.geolocation</code> API.
      </p>
      <LocationAction />
      <div style={{ height: 500, border: "1px solid #ddd" }}>
        <CopilotChat
          labels={{
            title: "Gantry demo",
            initial: "Ask me where you are.",
          }}
        />
      </div>
    </main>
  );
}
