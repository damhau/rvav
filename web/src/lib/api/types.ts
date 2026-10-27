// Short names for the generated schema types. Regenerate schema.d.ts with `pnpm gen:api`.
import type { components } from "./schema";

type S = components["schemas"];

export type Problem = S["Problem"];
export type FieldError = S["FieldError"];
export type UserRef = S["UserRef"];
export type Me = S["Me"];
export type ProjectRole = S["ProjectRole"];
export type EnvironmentKey = S["EnvironmentKey"];
export type Environment = S["Environment"];
export type ProjectRef = S["ProjectRef"];
export type Project = S["Project"];
export type ProjectPage = S["ProjectPage"];
export type QuotaResource = S["QuotaResource"];
export type QuotaUsage = S["QuotaUsage"];
export type CatalogItemType = S["CatalogItemType"];
export type CatalogCategory = S["CatalogCategory"];
export type ApprovalHint = S["ApprovalHint"];
export type CatalogItem = S["CatalogItem"];
export type CatalogItemPage = S["CatalogItemPage"];
export type RequestForm = S["RequestForm"];
export type UiSchema = S["UiSchema"];
export type UiSection = S["UiSection"];
export type UiWidget = S["UiWidget"];
export type UiField = S["UiField"];
export type UiRules = S["UiRules"];
export type ValueSource = S["ValueSource"];
export type Option = S["Option"];
export type FieldValuesQuery = S["FieldValuesQuery"];
export type FieldValues = S["FieldValues"];
export type RequestDraft = S["RequestDraft"];
export type RequestPreview = S["RequestPreview"];
export type QuotaImpact = S["QuotaImpact"];
export type ApprovalPreview = S["ApprovalPreview"];
export type RequestStatus = S["RequestStatus"];
export type ServiceRequest = S["Request"];
export type RequestPage = S["RequestPage"];
export type StepStatus = S["StepStatus"];
export type Step = S["Step"];
export type LogChunk = S["LogChunk"];
export type LogLine = S["LogChunk"]["lines"][number];
export type DeploymentStatus = S["DeploymentStatus"];
export type Deployment = S["Deployment"];
export type DeploymentDetail = S["DeploymentDetail"];
export type DeploymentPage = S["DeploymentPage"];
export type Lease = S["Lease"];
export type SyncState = S["SyncState"];
export type Resource = S["Resource"];
export type VmResource = S["VmResource"];
export type ConsoleSession = S["ConsoleSession"];
export type ActionForm = S["ActionForm"];
export type Day2Action = S["Day2Action"];
export type Day2Draft = S["Day2Draft"];
export type VmProperties = S["VmProperties"];
export type IpAddressResource = S["IpAddressResource"];
export type DnsRecordResource = S["DnsRecordResource"];
export type ApprovalStatus = S["ApprovalStatus"];
export type ApprovalSummary = S["ApprovalSummary"];
export type Approval = S["Approval"];
export type ApprovalVote = S["ApprovalVote"];
export type ApprovalEditableField = S["ApprovalEditableField"];
export type ApprovalDecision = S["ApprovalDecision"];
export type ApprovalPage = S["ApprovalPage"];
export type LiveEvent = S["Event"];
export type NotificationEvent = S["NotificationEvent"];
export type IpamReconcile = S["IpamReconcile"];
export type AwxTemplate = S["AwxTemplate"];
export type AwxAction = S["AwxAction"];
export type AwxActionUpdate = S["AwxActionUpdate"];

/** Every paginated list has this shape. */
export interface Page<T> {
  items: T[];
  next_cursor?: string | null;
}
