import { FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { Callout } from "@/components/Callout";
import { EmptyRow } from "@/components/EmptyState";
import { Shell } from "@/components/Shell";
import { instrumentClient } from "@/lib/clients";
import { WriteOnly, OwnerOnly, canWrite, useCurrentRole } from "@/lib/role";
import {
  Instrument,
  InstrumentKind,
  InstrumentStatus,
} from "@/gen/stillhouse/v1/instrument_pb";

const kindLabel: Record<InstrumentKind, string> = {
  [InstrumentKind.UNSPECIFIED]: "—",
  [InstrumentKind.THERMOMETER]: "Thermometer",
  [InstrumentKind.HYDROMETER]: "Hydrometer",
  [InstrumentKind.DENSITY_METER]: "Density meter",
  [InstrumentKind.MASS_FLOW_METER]: "Mass flow meter",
  [InstrumentKind.SCALE]: "Scale",
  [InstrumentKind.VOLUMETRIC_MEASURE]: "Volumetric measure",
  [InstrumentKind.OTHER]: "Other",
};

const statusLabel: Record<InstrumentStatus, string> = {
  [InstrumentStatus.UNSPECIFIED]: "—",
  [InstrumentStatus.ACTIVE]: "Active",
  [InstrumentStatus.SUSPENDED]: "Suspended",
  [InstrumentStatus.RETIRED]: "Retired",
};

const registerableKinds = [
  InstrumentKind.HYDROMETER,
  InstrumentKind.THERMOMETER,
  InstrumentKind.DENSITY_METER,
  InstrumentKind.MASS_FLOW_METER,
  InstrumentKind.SCALE,
  InstrumentKind.VOLUMETRIC_MEASURE,
  InstrumentKind.OTHER,
];

export function InstrumentsPage() {
  const qc = useQueryClient();
  const role = useCurrentRole();
  const writeable = canWrite(role);
  const [includeRetired, setIncludeRetired] = useState(false);
  const [showForm, setShowForm] = useState(false);
  const [calibrating, setCalibrating] = useState<Instrument | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const list = useQuery({
    queryKey: ["listInstruments", includeRetired],
    queryFn: () => instrumentClient.listInstruments({ includeRetired }),
  });

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["listInstruments"] });
  };

  const createInstrument = useMutation({
    mutationFn: (msg: Parameters<typeof instrumentClient.createInstrument>[0]) =>
      instrumentClient.createInstrument(msg),
    onSuccess: () => {
      setShowForm(false);
      setErr(null);
      invalidate();
    },
    onError: (e) => setErr(ConnectError.from(e).message),
  });

  const recordCalibration = useMutation({
    mutationFn: (msg: Parameters<typeof instrumentClient.recordCalibration>[0]) =>
      instrumentClient.recordCalibration(msg),
    onSuccess: () => {
      setCalibrating(null);
      setErr(null);
      invalidate();
    },
    onError: (e) => setErr(ConnectError.from(e).message),
  });

  const setStatus = useMutation({
    mutationFn: (msg: Parameters<typeof instrumentClient.setInstrumentStatus>[0]) =>
      instrumentClient.setInstrumentStatus(msg),
    onSuccess: () => {
      setErr(null);
      invalidate();
    },
    onError: (e) => setErr(ConnectError.from(e).message),
  });

  const instruments = list.data?.instruments ?? [];
  const unapproved = instruments.filter((i) => !i.usable && i.status === InstrumentStatus.ACTIVE);
  const overdue = instruments.filter((i) => i.calibrationOverdue && i.usable);

  const onCreate = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    const interval = Number(fd.get("calibrationIntervalDays") ?? 0);
    createInstrument.mutate({
      kind: Number(fd.get("kind")) as InstrumentKind,
      label: String(fd.get("label") ?? ""),
      manufacturer: String(fd.get("manufacturer") ?? ""),
      model: String(fd.get("model") ?? ""),
      serialNo: String(fd.get("serialNo") ?? ""),
      approvalReference: String(fd.get("approvalReference") ?? ""),
      approvalDate: String(fd.get("approvalDate") ?? ""),
      approvalExpiresOn: String(fd.get("approvalExpiresOn") ?? ""),
      calibrationIntervalDays: Number.isFinite(interval) ? interval : 0,
      notes: String(fd.get("notes") ?? ""),
    });
  };

  const onCalibrate = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!calibrating) return;
    const fd = new FormData(e.currentTarget);
    recordCalibration.mutate({
      instrumentId: calibrating.id,
      calibratedOn: String(fd.get("calibratedOn") ?? ""),
      performedBy: String(fd.get("performedBy") ?? ""),
      certificateRef: String(fd.get("certificateRef") ?? ""),
      passed: fd.get("passed") === "on",
      notes: String(fd.get("notes") ?? ""),
    });
  };

  return (
    <Shell>
      <header className="mb-6">
        <h1 className="text-2xl font-semibold text-fg">Instruments</h1>
        <p className="mt-1 text-sm text-fg-muted">
          Volume and absolute alcohol content must be determined with CRA-approved
          instruments, and each individual instrument must itself be approved —
          approval attaches to the serial number, not the model (EDM3-1-1 ¶24,
          EDM1-1-5). This register is what a gauge points at.
        </p>
      </header>

      {err && (
        <Callout tone="danger" title="That didn't work">
          {err}
        </Callout>
      )}

      {unapproved.length > 0 && (
        <Callout tone="warning" title={`${unapproved.length} instrument${unapproved.length === 1 ? "" : "s"} cannot be used`}>
          An instrument with no CRA approval on file will be refused if a gauge names
          it. Add its approval reference, or leave it off the determination.
        </Callout>
      )}
      {overdue.length > 0 && (
        <Callout tone="warning" title={`${overdue.length} instrument${overdue.length === 1 ? "" : "s"} past due for calibration`}>
          These still work — an approved instrument that is overdue for a check is a
          compliance risk, not a false reading — but every determination made with
          one carries a warning until it is recalibrated.
        </Callout>
      )}

      <div className="mb-4 flex items-center justify-between gap-3">
        <label className="flex items-center gap-2 text-sm text-fg-muted">
          <input
            type="checkbox"
            checked={includeRetired}
            onChange={(e) => setIncludeRetired(e.target.checked)}
          />
          Show retired
        </label>
        <WriteOnly>
          <button
            onClick={() => setShowForm((v) => !v)}
            className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover"
          >
            {showForm ? "Cancel" : "Register an instrument"}
          </button>
        </WriteOnly>
      </div>

      {showForm && (
        <form
          onSubmit={onCreate}
          className="mb-6 grid grid-cols-1 gap-4 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-2"
        >
          <Field label="Kind">
            <select name="kind" required defaultValue={InstrumentKind.HYDROMETER} className={inputCls}>
              {registerableKinds.map((k) => (
                <option key={k} value={k}>
                  {kindLabel[k]}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Label (what you call it on the floor)">
            <input name="label" required placeholder="Still house hydro #2" className={inputCls} />
          </Field>
          <Field label="Serial number">
            <input name="serialNo" required className={inputCls} />
          </Field>
          <Field label="CRA approval reference">
            <input name="approvalReference" placeholder="leave blank until approved" className={inputCls} />
          </Field>
          <Field label="Manufacturer">
            <input name="manufacturer" className={inputCls} />
          </Field>
          <Field label="Model">
            <input name="model" className={inputCls} />
          </Field>
          <Field label="Approval date">
            <input name="approvalDate" type="date" className={inputCls} />
          </Field>
          <Field label="Approval expires">
            <input name="approvalExpiresOn" type="date" className={inputCls} />
          </Field>
          <Field label="Calibration interval (days)">
            <input
              name="calibrationIntervalDays"
              type="number"
              min={0}
              placeholder="blank = no interval set"
              className={inputCls}
            />
          </Field>
          <Field label="Notes">
            <input name="notes" className={inputCls} />
          </Field>
          <div className="sm:col-span-2">
            <button
              type="submit"
              disabled={createInstrument.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {createInstrument.isPending ? "Registering…" : "Register"}
            </button>
          </div>
        </form>
      )}

      <div className="overflow-x-auto rounded-lg border border-border">
        <table className="min-w-full text-sm">
          <thead className="bg-surface-3 text-left text-xs uppercase text-fg-muted">
            <tr>
              <th className="px-3 py-2">Instrument</th>
              <th className="px-3 py-2">Serial</th>
              <th className="px-3 py-2">CRA approval</th>
              <th className="px-3 py-2">Calibration</th>
              <th className="px-3 py-2">Status</th>
              {writeable && <th className="px-3 py-2" />}
            </tr>
          </thead>
          <tbody>
            {instruments.length === 0 && (
              <EmptyRow
                colSpan={writeable ? 6 : 5}
                title="No instruments registered"
                message="A gauge can still be recorded without naming one — it is simply recorded as naming none."
              />
            )}
            {instruments.map((i) => (
              <tr key={i.id} className="border-t border-border">
                <td className="px-3 py-2">
                  <div className="font-medium text-fg">{i.label}</div>
                  <div className="text-xs text-fg-muted">
                    {kindLabel[i.kind]}
                    {i.manufacturer || i.model ? ` · ${[i.manufacturer, i.model].filter(Boolean).join(" ")}` : ""}
                  </div>
                </td>
                <td className="px-3 py-2 font-mono text-xs">{i.serialNo}</td>
                <td className="px-3 py-2">
                  {i.approvalReference ? (
                    <>
                      <div>{i.approvalReference}</div>
                      {i.approvalExpiresOn && (
                        <div className="text-xs text-fg-muted">expires {i.approvalExpiresOn}</div>
                      )}
                    </>
                  ) : (
                    <span className="text-warning">none on file</span>
                  )}
                </td>
                <td className="px-3 py-2">
                  {i.lastCalibratedOn ? (
                    <>
                      <div>{i.lastCalibratedOn}</div>
                      {i.calibrationDueOn && (
                        <div className={`text-xs ${i.calibrationOverdue ? "text-warning" : "text-fg-muted"}`}>
                          {i.calibrationOverdue ? "overdue since " : "due "}
                          {i.calibrationDueOn}
                        </div>
                      )}
                    </>
                  ) : (
                    <span className="text-xs text-fg-muted">
                      {i.calibrationIntervalDays > 0 ? "never — overdue" : "no interval set"}
                    </span>
                  )}
                </td>
                <td className="px-3 py-2">
                  <div>{statusLabel[i.status]}</div>
                  {!i.usable && (
                    <div className="text-xs text-warning" title={i.unusableReason}>
                      not usable for a determination
                    </div>
                  )}
                </td>
                {writeable && (
                  <td className="px-3 py-2 text-right">
                    <button
                      onClick={() => setCalibrating(i)}
                      className="rounded border border-border-strong px-2 py-1 text-xs hover:bg-surface-3"
                    >
                      Calibrate
                    </button>
                    <OwnerOnly>
                      {i.status === InstrumentStatus.ACTIVE ? (
                        <button
                          onClick={() => {
                            const reason = window.prompt("Why is this instrument being withdrawn?");
                            if (!reason) return;
                            setStatus.mutate({
                              id: i.id,
                              status: InstrumentStatus.SUSPENDED,
                              reason,
                            });
                          }}
                          className="ml-2 rounded border border-border-strong px-2 py-1 text-xs hover:bg-surface-3"
                        >
                          Suspend
                        </button>
                      ) : (
                        <button
                          onClick={() =>
                            setStatus.mutate({ id: i.id, status: InstrumentStatus.ACTIVE, reason: "" })
                          }
                          className="ml-2 rounded border border-border-strong px-2 py-1 text-xs hover:bg-surface-3"
                        >
                          Return to service
                        </button>
                      )}
                    </OwnerOnly>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {calibrating && (
        <form
          onSubmit={onCalibrate}
          className="mt-6 grid grid-cols-1 gap-4 rounded-lg border border-border bg-surface-2 p-5 sm:grid-cols-2"
        >
          <div className="sm:col-span-2 text-sm font-semibold text-fg">
            Record a calibration — {calibrating.label} ({calibrating.serialNo})
          </div>
          <Field label="Calibrated on">
            <input name="calibratedOn" type="date" className={inputCls} />
          </Field>
          <Field label="Performed by">
            <input name="performedBy" placeholder="Measurement Canada, in-house…" className={inputCls} />
          </Field>
          <Field label="Certificate reference">
            <input name="certificateRef" className={inputCls} />
          </Field>
          <Field label="Notes">
            <input name="notes" className={inputCls} />
          </Field>
          <label className="flex items-center gap-2 text-sm sm:col-span-2">
            <input name="passed" type="checkbox" defaultChecked />
            {/* A failure is history worth keeping, but it is not the date the
                next check is counted from — an instrument that failed has not
                been calibrated. */}
            Passed. Leave unchecked to record a failed check: it is kept as history
            but does not reset the calibration clock.
          </label>
          <div className="flex items-center gap-3 sm:col-span-2">
            <button
              type="submit"
              disabled={recordCalibration.isPending}
              className="rounded bg-accent px-3 py-2 text-sm font-medium text-white hover:bg-accent-hover disabled:bg-accent/50"
            >
              {recordCalibration.isPending ? "Saving…" : "Record"}
            </button>
            <button
              type="button"
              onClick={() => setCalibrating(null)}
              className="rounded border border-border-strong px-3 py-2 text-sm hover:bg-surface-3"
            >
              Cancel
            </button>
          </div>
        </form>
      )}
    </Shell>
  );
}

const inputCls = "w-full rounded border border-border-strong px-3 py-2 text-sm";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="mb-2 block text-sm font-medium text-fg-muted">{label}</label>
      {children}
    </div>
  );
}
