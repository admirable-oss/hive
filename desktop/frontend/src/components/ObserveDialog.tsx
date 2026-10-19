import { Eye, Loader2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DialogFooter } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { api } from "../api";
import { store, useStore } from "../store";
import { Field, NativeSelect } from "./Field";
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
    <Modal
      title="Open an observer window"
      description="For a shared screen or a second monitor. It shows agents and, if you allow it, their screens, and cannot type or change anything. It gets its own observer credential, which ends when the window closes."
    >
      <form onSubmit={submit} className="grid gap-4">
        <fieldset className="grid gap-2.5">
          <legend className="mb-2 text-sm font-medium">It may see</legend>
          <RadioGroup value={all ? "all" : "some"} onValueChange={(v) => setAll(v === "all")}>
            <Label className="font-normal">
              <RadioGroupItem value="all" /> Everything on this computer
            </Label>
            <Label className="font-normal">
              <RadioGroupItem value="some" /> These environments
            </Label>
          </RadioGroup>
          {!all && (
            <div className="ml-6 grid gap-2">
              {envs.map((e) => (
                <Label key={e.id} className="font-normal">
                  <Checkbox checked={chosen.includes(e.id)} onCheckedChange={(on) => setChosen((c) => (on ? [...c, e.id] : c.filter((x) => x !== e.id)))} />
                  {e.name}
                </Label>
              ))}
            </div>
          )}
        </fieldset>
        <Label className="font-normal">
          <Checkbox checked={screens} onCheckedChange={(on) => setScreens(on === true)} /> Show screens <span className="text-muted-foreground">(otherwise states only)</span>
        </Label>
        <Field label="For">
          <NativeSelect value={hours} onChange={(e) => setHours(Number(e.target.value))}>
            {durations.map((h) => (
              <option key={h} value={h}>
                {h < 24 ? `${h} hour${h > 1 ? "s" : ""}` : `${h / 24} day${h > 24 ? "s" : ""}`}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button type="submit" disabled={busy || (!all && chosen.length === 0)}>
            {busy ? <Loader2 className="animate-spin" /> : <Eye />}
            Open window
          </Button>
        </DialogFooter>
      </form>
    </Modal>
  );
}
