export type NoticeAction = { label: string; onClick: () => void; primary?: boolean };

export type NoticePlacement = "screen" | "top" | "bottom";

export function Notice(props: { message: string; actions?: NoticeAction[]; placement?: NoticePlacement }) {
  const placement = props.placement ?? "screen";
  const card = (
    <div role="alert" className={`notice${placement === "screen" ? "" : ` notice-${placement}`}`}>
      <p className="notice-msg">{props.message}</p>
      {props.actions && props.actions.length > 0 && (
        <div className="notice-actions">
          {props.actions.map((a) => (
            <button key={a.label} type="button" className={`notice-btn${a.primary ? " primary" : ""}`} onClick={a.onClick}>{a.label}</button>
          ))}
        </div>
      )}
    </div>
  );
  if (placement !== "screen") return card;
  return <div className="notice-screen-wrap">{card}</div>;
}
