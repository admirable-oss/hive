import { useState } from "react";
import { api } from "../api";
import { store, useStore } from "../store";
import { close, Modal } from "./Modal";

const durations = [1, 8, 24, 72, 168];

// ObserveDialog opens a read-only window for a shared screen, with an
// observer credential of its own.
export function ObserveDialog() {
  const envs = useStore((s) => s.workspace.environments).filter((e) => !e.machine);
  const [all, setAll] = useState(true);
  const [chosen, setChosen] = useState<string[]>([]);
  const [screens, setScreens] = useState(true);
  const [hours, setHours] = useState(8);
  const [busy, setBusy] = useState(false);
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    api
      .openObserver({ all, environments: all ? undefined : chosen, screens, hours })
      .then(close)
      .catch((err) => {
        setBusy(false);
        store.fail(err);
      });
  };
  return (
    <Modal title="Open an observer window" onClose={close}>
      <form onSubmit={submit}>
        <p className="quiet">
          A window for a shared screen or a second monitor. It shows agents and, if you allow it, their screens, and cannot type or
          change anything. It gets its own observer credential, which ends when the window closes.
        </p>
        <fieldset className="choice">
          <legend>It may see</legend>
          <label className="inline">
            <input type="radio" checked={all} onChange={() => setAll(true)} /> Everything on this computer
          </label>
          <label className="inline">
            <input type="radio" checked={!all} onChange={() => setAll(false)} /> These environments:
          </label>
          {!all && (
            <div className="env-choices">
              {envs.map((e) => (
                <label key={e.id} className="inline">
                  <input
                    type="checkbox"
                    checked={chosen.includes(e.id)}
                    onChange={(ev) => setChosen((c) => (ev.target.checked ? [...c, e.id] : c.filter((x) => x !== e.id)))}
                  />{" "}
                  {e.name}
                </label>
              ))}
            </div>
          )}
        </fieldset>
        <label className="inline">
          <input type="checkbox" checked={screens} onChange={(e) => setScreens(e.target.checked)} /> Show screens (otherwise
          states only)
        </label>
        <label>
          For
          <select value={hours} onChange={(e) => setHours(Number(e.target.value))}>
            {durations.map((h) => (
              <option key={h} value={h}>
                {h < 24 ? `${h} hour${h > 1 ? "s" : ""}` : `${h / 24} day${h > 24 ? "s" : ""}`}
              </option>
            ))}
          </select>
        </label>
        <div className="buttons">
          <button type="button" onClick={close}>
            Cancel
          </button>
          <button className="primary" type="submit" disabled={busy || (!all && chosen.length === 0)}>
            Open window
          </button>
        </div>
      </form>
    </Modal>
  );
}
