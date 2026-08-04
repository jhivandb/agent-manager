/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  useDeleteMCPProxyScope,
  useListMCPProxyScopes,
  useUpdateMCPProxyScope,
} from "@agent-management-platform/api-client";
import { useConfirmationDialog } from "@agent-management-platform/shared-component";
import type {
  APIKeyLocation,
  Environment,
  MCPEndpointConfig,
  MCPProxy,
  MCPProxyScopeResponse,
} from "@agent-management-platform/types";
import {
  Alert,
  Autocomplete,
  Button,
  Chip,
  Collapse,
  FormControl,
  FormLabel,
  Grid,
  IconButton,
  ListingTable,
  MenuItem,
  Select,
  Skeleton,
  Stack,
  Tab,
  Tabs,
  TextField,
  Tooltip,
  Typography,
} from "@wso2/oxygen-ui";
import { Plus, ShieldX, Trash } from "@wso2/oxygen-ui-icons-react";
import {
  type AuthenticationType,
  getAuthenticationTypeLabel,
  getCapabilityId,
  isToolBlockedByAcl,
  resolveAuthenticationType,
} from "./mcpEndpoints";
import { CreateScopeDrawer } from "./CreateScopeDrawer";

const KEY_LOCATION_OPTIONS: { value: APIKeyLocation; label: string }[] = [
  { value: "header", label: "header" },
  { value: "query", label: "query" },
];

const AUTHENTICATION_TYPE_OPTIONS: AuthenticationType[] = [
  "",
  "apiKey",
  "identity",
];

// A local editable row for the identity-security tool-scope-binding table.
// One row per tool discovered on this endpoint — every tool always has a
// row, whether or not it currently has any scopes assigned — so the tool
// itself doubles as the row's key.
type ToolScopeRow = {
  tool: string;
  scopes: MCPProxyScopeResponse[];
};

// Diffs the desired (action -> tools) mapping built from the current rows
// against the last-fetched scope list, producing only the tool-list updates
// needed to bring the server in sync. Scopes themselves are created via the
// Create Scope panel and deleted explicitly from the scopes list below — a
// scope ending up with zero tools here is still a valid, saved state, not a
// signal to delete it.
function computeScopeToolUpdates(
  rows: ToolScopeRow[],
  catalogScopes: MCPProxyScopeResponse[],
): { action: string; tools: string[] }[] {
  const desired = new Map<string, Set<string>>(
    catalogScopes.map((s) => [s.action, new Set<string>()]),
  );
  for (const row of rows) {
    for (const scope of row.scopes) {
      desired.get(scope.action)?.add(row.tool);
    }
  }
  const setsEqual = (a: Set<string>, b: Set<string>) =>
    a.size === b.size && [...a].every((v) => b.has(v));

  const updates: { action: string; tools: string[] }[] = [];
  for (const scope of catalogScopes) {
    const desiredTools = desired.get(scope.action) ?? new Set<string>();
    if (!setsEqual(desiredTools, new Set(scope.tools))) {
      updates.push({ action: scope.action, tools: [...desiredTools] });
    }
  }
  return updates;
}

export type MCPProxySecurityTabProps = {
  config: MCPEndpointConfig | undefined;
  selectedEndpointId: string;
  orgName: string | undefined;
  proxyId: string | undefined;
  /**
   * Environments the selected endpoint is deployed to, for the Create Scope
   * panel's role picker.
   */
  environments: Environment[];
  isLoading?: boolean;
  onUpdate: (fields: Partial<MCPEndpointConfig>) => Promise<MCPProxy>;
  isUpdating: boolean;
};

export function MCPProxySecurityTab({
  config,
  selectedEndpointId,
  orgName,
  proxyId,
  environments,
  isLoading = false,
  onUpdate,
  isUpdating,
}: MCPProxySecurityTabProps) {
  const [authenticationType, setAuthenticationType] =
    useState<AuthenticationType>("apiKey");
  const [keyValue, setKeyValue] = useState("");
  const [keyIn, setKeyIn] = useState<APIKeyLocation>("header");
  const [status, setStatus] = useState<{
    message: string;
    severity: "success" | "error";
  } | null>(null);
  const [fieldErrors, setFieldErrors] = useState<{ keyValue?: string }>({});
  const [createScopeDrawerOpen, setCreateScopeDrawerOpen] = useState(false);
  const [authorizationTab, setAuthorizationTab] = useState<"tools" | "scopes">("tools");
  const { addConfirmation } = useConfirmationDialog();

  // Tracks what was last confirmed persisted (seeded from config, refreshed on
  // save) rather than re-deriving "saved" straight from the config prop on
  // every render — config only reflects a save once its background refetch
  // lands, which would otherwise leave authIsDirty true for a beat after a
  // successful save.
  const lastSavedAuthRef = useRef<{
    type: AuthenticationType;
    key: string;
    in: APIKeyLocation;
  }>({ type: "apiKey", key: "", in: "header" });

  const authIsDirty = useMemo(() => {
    if (!config) return false;
    const saved = lastSavedAuthRef.current;
    if (authenticationType !== saved.type) return true;
    if (keyValue.trim() !== saved.key) return true;
    if (keyIn !== saved.in) return true;
    return false;
  }, [config, authenticationType, keyValue, keyIn]);

  useEffect(() => {
    if (!config || !selectedEndpointId) return;
    const nextType = resolveAuthenticationType(config);
    const nextKey =
      config.security?.apiKey?.key ?? (nextType === "apiKey" ? "X-API-Key" : "");
    const nextIn = (config.security?.apiKey?.in as APIKeyLocation) ?? "header";
    setAuthenticationType(nextType);
    setKeyValue(nextKey);
    setKeyIn(nextIn);
    setFieldErrors({});
    lastSavedAuthRef.current = { type: nextType, key: nextKey, in: nextIn };
  }, [config, selectedEndpointId]);

  // --- Agent Identity: per-tool scope-binding (RBAC) state ---

  const toolEntries = useMemo(() => {
    const identifiers: string[] = [];
    for (const raw of config?.capabilities?.tools ?? []) {
      const identifier = getCapabilityId("tool", raw);
      if (identifier) identifiers.push(identifier);
    }
    return identifiers;
  }, [config?.capabilities?.tools]);

  // Computed once per tool list / ACL policy change rather than per row and per
  // dropdown option on every render — isToolBlockedByAcl re-parses the ACL
  // policy's params each call, and every row's Select renders one option per tool.
  const blockedToolIds = useMemo(
    () =>
      new Set(
        toolEntries.filter((identifier) =>
          isToolBlockedByAcl(config, identifier),
        ),
      ),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only reads config.policies
    [toolEntries, config?.policies],
  );

  const { data: scopesData, isPending: scopesPending } = useListMCPProxyScopes(
    { orgName: orgName ?? "", proxyId: proxyId ?? "" },
    { enabled: authenticationType === "identity" && !!proxyId },
  );
  // The persisted source of truth: what the tool-scope table diffs against on
  // Save, and the only source of options the Autocomplete offers (scopes are
  // created via the Create Scope panel, not from this table).
  const catalogScopes: MCPProxyScopeResponse[] = useMemo(
    () => scopesData?.scopes ?? [],
    [scopesData],
  );
  const updateMCPProxyScope = useUpdateMCPProxyScope();
  const deleteMCPProxyScope = useDeleteMCPProxyScope();

  // One row per known tool, pre-populated from the endpoint's discovered
  // tool list so scopes can be assigned directly — no separate "add tool"
  // step. Starts empty and is seeded by the effects below once the tool
  // list and scope catalog are both available.
  const [toolScopeRows, setToolScopeRows] = useState<ToolScopeRow[]>([]);
  const lastSavedToolScopeRowsRef = useRef<ToolScopeRow[]>([]);

  const serializeToolScopeRows = (rows: ToolScopeRow[]) =>
    JSON.stringify(
      rows.map((row) => ({
        tool: row.tool,
        scopes: [...row.scopes.map((s) => s.action)].sort(),
      })),
    );

  const toolScopesDirty = useMemo(() => {
    return (
      serializeToolScopeRows(toolScopeRows) !==
      serializeToolScopeRows(lastSavedToolScopeRowsRef.current)
    );
  }, [toolScopeRows]);

  // Rows are a view built from the discovered tool list, not a separately
  // stored binding: every known tool always has a row, seeded with whichever
  // scopes already reference it (empty if none). Memoized since both reseed
  // effects below would otherwise rebuild this from scratch on every render
  // they fire in, even when neither toolEntries nor catalogScopes changed.
  const derivedToolScopeRows = useMemo<ToolScopeRow[]>(() => {
    const toolToScopes = new Map<string, MCPProxyScopeResponse[]>();
    for (const scope of catalogScopes) {
      for (const tool of scope.tools) {
        const scopesForTool = toolToScopes.get(tool) ?? [];
        scopesForTool.push(scope);
        toolToScopes.set(tool, scopesForTool);
      }
    }
    return toolEntries.map((tool) => ({
      tool,
      scopes: toolToScopes.get(tool) ?? [],
    }));
  }, [toolEntries, catalogScopes]);

  // Switching endpoint tabs discards unsaved row edits, consistent with the
  // auth-fields effect above — even though scopes are shared across every
  // endpoint of the proxy, this tab's Save/Discard state is still per endpoint.
  // Reads derivedToolScopeRows without depending on it — the effect below
  // owns reseeding on scope-list changes, so this one only reacts to the tab switch.
  useEffect(() => {
    if (!selectedEndpointId) return;
    setToolScopeRows(derivedToolScopeRows);
    lastSavedToolScopeRowsRef.current = derivedToolScopeRows;
    // eslint-disable-next-line react-hooks/exhaustive-deps -- handled by the effect below
  }, [selectedEndpointId]);

  // Reseed when the scope list refetches (e.g. right after Save invalidates
  // the query), but only while there are no unsaved edits — otherwise this
  // would clobber in-progress changes on a stray background refetch. Doesn't
  // depend on selectedEndpointId — the effect above already handles tab
  // switches, and scopes are proxy-level so switching tabs alone never
  // changes catalogScopes.
  useEffect(() => {
    if (!selectedEndpointId || toolScopesDirty) return;
    setToolScopeRows(derivedToolScopeRows);
    lastSavedToolScopeRowsRef.current = derivedToolScopeRows;
    // eslint-disable-next-line react-hooks/exhaustive-deps -- guard, not a trigger dep
  }, [catalogScopes]);

  const setRowScopes = useCallback((tool: string, scopes: MCPProxyScopeResponse[]) => {
    setToolScopeRows((prev) =>
      prev.map((row) => (row.tool === tool ? { ...row, scopes } : row)),
    );
  }, []);

  const handleToolScopeRowScopesChange = useCallback(
    (tool: string, options: MCPProxyScopeResponse[]) => {
      setRowScopes(tool, options);
    },
    [setRowScopes],
  );

  // Confirm before switching methods — bound agents' credentials are reconciled
  // automatically and their pods restart. Reads the saved type from `config`, not
  // lastSavedAuthRef, which defaults to "apiKey" until the sync effect above runs and
  // would otherwise warn about a method nobody actually configured.
  const handleAuthTypeChange = useCallback(
    (nextType: AuthenticationType) => {
      const savedType = resolveAuthenticationType(config);
      if (savedType && nextType !== savedType) {
        addConfirmation({
          title: "Switch authentication method?",
          description: `This proxy is currently secured with ${getAuthenticationTypeLabel(savedType)}. Switching to ${getAuthenticationTypeLabel(nextType)} will update every agent already configured to use it — their credentials are reconciled automatically and their pods restart to pick up the change.`,
          confirmButtonText: "Switch Method",
          confirmButtonColor: "error",
          onConfirm: () => setAuthenticationType(nextType),
        });
        return;
      }
      setAuthenticationType(nextType);
    },
    [addConfirmation, config],
  );

  const isDirty = authIsDirty || toolScopesDirty;

  const handleDiscard = useCallback(() => {
    if (!config) return;
    const nextType = resolveAuthenticationType(config);
    setAuthenticationType(nextType);
    setKeyValue(
      config.security?.apiKey?.key ??
      (nextType === "apiKey" ? "X-API-Key" : ""),
    );
    setKeyIn((config.security?.apiKey?.in as APIKeyLocation) ?? "header");
    setFieldErrors({});
    setStatus(null);

    setToolScopeRows(lastSavedToolScopeRowsRef.current);
  }, [config]);

  const handleSave = useCallback(async () => {
    if (!config) return;

    if (authenticationType === "apiKey" && keyValue.trim().length === 0) {
      const message = "API Key is required when using API Key authentication";
      setFieldErrors({ keyValue: message });
      setStatus({ message, severity: "error" });
      return;
    }
    setFieldErrors({});

    try {
      await onUpdate({
        security: {
          enabled: config.security?.enabled ?? true,
          apiKey: {
            enabled: authenticationType === "apiKey",
            key: authenticationType === "apiKey" ? keyValue.trim() : "",
            in: keyIn,
          },
          identity: {
            enabled: authenticationType === "identity",
          },
        },
      });

      // Record the auth snapshot as soon as the auth mutation itself
      // succeeds — independent of whether the scope-update step below
      // (a separate set of REST calls) succeeds, so a failure there doesn't
      // leave the already-saved auth settings looking dirty.
      lastSavedAuthRef.current = {
        type: authenticationType,
        key: authenticationType === "apiKey" ? keyValue.trim() : "",
        in: keyIn,
      };

      // Scopes belong to the proxy, not this endpoint's auth mode, and are
      // saved via their own REST calls rather than bundled into the security
      // payload above.
      if (authenticationType === "identity" && toolScopesDirty && orgName && proxyId) {
        const updates = computeScopeToolUpdates(toolScopeRows, catalogScopes);

        await Promise.all(
          updates.map((u) =>
            updateMCPProxyScope.mutateAsync({
              params: { orgName, proxyId, scopeAction: u.action },
              body: { tools: u.tools },
            }),
          ),
        );

        lastSavedToolScopeRowsRef.current = toolScopeRows;
      }

      setStatus({
        message: "Updated security settings.",
        severity: "success",
      });
    } catch {
      setStatus({
        message: "Failed to update security.",
        severity: "error",
      });
    }
  }, [
    config,
    authenticationType,
    keyValue,
    keyIn,
    toolScopeRows,
    toolScopesDirty,
    catalogScopes,
    orgName,
    proxyId,
    onUpdate,
    updateMCPProxyScope,
  ]);

  const handleDeleteScope = useCallback(
    (scope: MCPProxyScopeResponse) => {
      if (!orgName || !proxyId) return;
      addConfirmation({
        title: "Delete Scope",
        description: `Are you sure you want to delete "${scope.action}"? Any tool it's currently assigned to will lose that requirement, and any role granted it will lose access through it. This action cannot be undone.`,
        confirmButtonText: "Delete",
        confirmButtonColor: "error",
        onConfirm: async () => {
          await deleteMCPProxyScope.mutateAsync({
            orgName,
            proxyId,
            scopeAction: scope.action,
          });
          // Drop it from any row referencing it immediately — the reseed
          // effect above only reseeds while there are no unsaved edits, and a
          // deleted scope shouldn't linger as a selectable/selected option.
          // Also drop it from the last-saved snapshot: it's been deleted on
          // the server, so keeping it there would make toolScopesDirty
          // compare against a stale scope and let Discard resurrect it.
          const dropDeletedScope = (rows: ToolScopeRow[]) =>
            rows.map((row) => ({
              ...row,
              scopes: row.scopes.filter((s) => s.action !== scope.action),
            }));
          setToolScopeRows(dropDeletedScope);
          lastSavedToolScopeRowsRef.current = dropDeletedScope(
            lastSavedToolScopeRowsRef.current,
          );
        },
      });
    },
    [orgName, proxyId, addConfirmation, deleteMCPProxyScope],
  );

  const isDisabled = isLoading || !config;
  const noToolsForRbac = !isLoading && !!config && toolEntries.length === 0;

  if (isLoading) {
    return (
      <Stack spacing={2}>
        <Typography variant="h6">Authentication</Typography>
        <Stack spacing={2}>
          {[1, 2, 3].map((i) => (
            <Stack key={i} spacing={0.5}>
              <Skeleton variant="text" width={120} height={16} />
              <Skeleton variant="rounded" height={40} />
            </Stack>
          ))}
        </Stack>
      </Stack>
    );
  }

  if (!config) {
    return null;
  }

  return (
    <Stack spacing={2}>
      <Typography variant="h6">Authentication</Typography>

      <Grid container spacing={3}>
        <Grid size={{ xs: 12, md: 5 }}>
          <FormControl fullWidth disabled={isDisabled}>
            <FormLabel>Method</FormLabel>
            <Select
              size="small"
              displayEmpty
              value={authenticationType || ""}
              onChange={(e) =>
                handleAuthTypeChange((e.target.value as AuthenticationType) || "")
              }
            >
              {AUTHENTICATION_TYPE_OPTIONS.map((type) => (
                <MenuItem key={type || "none"} value={type}>
                  {getAuthenticationTypeLabel(type)}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
        </Grid>
      </Grid>

      {authenticationType === "identity" && (
        <Stack spacing={2}>
          <Stack spacing={0.5}>
            <Typography variant="h6">Authorization</Typography>
            <Typography variant="body2" color="text.secondary">
              Restrict access to individual tools by assigning catalog
              scopes. Callers need a token that includes every scope
              required by a tool to invoke it.
            </Typography>
          </Stack>

          <Stack direction="row" alignItems="center" justifyContent="flex-end">
            <Tooltip
              title={
                noToolsForRbac
                  ? "This MCP Server has no tools. A scope must authorize at least one."
                  : ""
              }
            >
              <span>
                <Button
                  variant="outlined"
                  size="small"
                  startIcon={<Plus size={16} />}
                  onClick={() => setCreateScopeDrawerOpen(true)}
                  disabled={noToolsForRbac}
                >
                  Create Scope
                </Button>
              </span>
            </Tooltip>
          </Stack>

          <Tabs
            value={authorizationTab}
            onChange={(_e, value) => setAuthorizationTab(value)}
            sx={{ borderBottom: 1, borderColor: "divider" }}
          >
            <Tab label="Tools" value="tools" />
            <Tab label="Scopes" value="scopes" />
          </Tabs>

          {authorizationTab === "tools" &&
            (noToolsForRbac ? (
              <ListingTable.Container>
                <ListingTable.EmptyState
                  illustration={<ShieldX size={64} />}
                  title="No Tools Available"
                  description="This MCP Server has no tools. Scope bindings require at least one tool."
                />
              </ListingTable.Container>
            ) : (
              <ListingTable.Container>
                <ListingTable density="compact">
                  <ListingTable.Head>
                    <ListingTable.Row>
                      <ListingTable.Cell width="30%">Tool</ListingTable.Cell>
                      <ListingTable.Cell>Scopes</ListingTable.Cell>
                    </ListingTable.Row>
                  </ListingTable.Head>
                  <ListingTable.Body>
                    {toolScopeRows.map((row) => (
                      <ListingTable.Row key={row.tool}>
                        <ListingTable.Cell>
                          <Stack direction="row" alignItems="center" spacing={1}>
                            <Typography
                              component="span"
                              variant="body2"
                              sx={{ fontFamily: "monospace" }}
                              noWrap
                            >
                              {row.tool}
                            </Typography>
                            {blockedToolIds.has(row.tool) && (
                              <Tooltip title="Blocked by Manage Tools">
                                <Stack color="warning.main" direction="row" alignItems="center">
                                  <ShieldX size={14} />
                                </Stack>
                              </Tooltip>
                            )}
                          </Stack>
                        </ListingTable.Cell>
                        <ListingTable.Cell>
                          <Autocomplete
                            multiple
                            size="small"
                            disableCloseOnSelect
                            disabled={isDisabled || isUpdating}
                            options={catalogScopes}
                            value={row.scopes}
                            onChange={(_e, value) =>
                              handleToolScopeRowScopesChange(row.tool, value)
                            }
                            getOptionLabel={(option) => option.action}
                            isOptionEqualToValue={(option, value) => option.action === value.action}
                            renderTags={(value, getTagProps) =>
                              value.map((option, index) => (
                                <Chip
                                  {...getTagProps({ index })}
                                  key={option.action}
                                  label={option.action}
                                  size="small"
                                />
                              ))
                            }
                            renderInput={(params) => (
                              <TextField {...params} placeholder="Assign scopes..." />
                            )}
                            noOptionsText="No scopes in the catalog"
                            sx={{ minWidth: 280 }}
                          />
                        </ListingTable.Cell>
                      </ListingTable.Row>
                    ))}
                  </ListingTable.Body>
                </ListingTable>
              </ListingTable.Container>
            ))}

          {authorizationTab === "scopes" &&
            (!scopesPending && catalogScopes.length === 0 ? (
              <ListingTable.Container>
                <ListingTable.EmptyState
                  illustration={<ShieldX size={64} />}
                  title="No scopes yet"
                  description='Click "Create Scope" to add one.'
                />
              </ListingTable.Container>
            ) : (
              <ListingTable.Container>
                <ListingTable density="compact">
                  <ListingTable.Head>
                    <ListingTable.Row>
                      <ListingTable.Cell width="30%">Name</ListingTable.Cell>
                      <ListingTable.Cell>Description</ListingTable.Cell>
                      <ListingTable.Cell align="center" width="60px" />
                    </ListingTable.Row>
                  </ListingTable.Head>
                  <ListingTable.Body>
                    {catalogScopes.map((scope) => (
                      <ListingTable.Row key={scope.action}>
                        <ListingTable.Cell>
                          <Typography variant="body2" sx={{ fontFamily: "monospace" }}>
                            {scope.action}
                          </Typography>
                        </ListingTable.Cell>
                        <ListingTable.Cell>{scope.description || "-"}</ListingTable.Cell>
                        <ListingTable.Cell align="center">
                          <Tooltip title="Delete scope">
                            <IconButton
                              size="small"
                              color="error"
                              onClick={() => handleDeleteScope(scope)}
                            >
                              <Trash size={16} />
                            </IconButton>
                          </Tooltip>
                        </ListingTable.Cell>
                      </ListingTable.Row>
                    ))}
                  </ListingTable.Body>
                </ListingTable>
              </ListingTable.Container>
            ))}
        </Stack>
      )}

      {authenticationType === "apiKey" && (
        <Grid container spacing={3}>
          <Grid size={{ xs: 12, md: 5 }}>
            <FormControl fullWidth disabled={isDisabled}>
              <FormLabel>Key Location</FormLabel>
              <Select
                size="small"
                value={keyIn}
                onChange={(e) => setKeyIn(e.target.value as APIKeyLocation)}
              >
                {KEY_LOCATION_OPTIONS.map((opt) => (
                  <MenuItem key={opt.value} value={opt.value}>
                    {opt.label}
                  </MenuItem>
                ))}
              </Select>
            </FormControl>
          </Grid>
          <Grid size={{ xs: 12, md: 5 }}>
            <FormControl
              fullWidth
              disabled={isDisabled}
              error={!!fieldErrors.keyValue}
            >
              <FormLabel>
                {keyIn === "query" ? "Query Param Key" : "Header Key"}
              </FormLabel>
              <TextField
                size="small"
                value={keyValue}
                onChange={(e) => {
                  setKeyValue(e.target.value);
                  if (fieldErrors.keyValue) setFieldErrors({});
                }}
                error={!!fieldErrors.keyValue}
                helperText={fieldErrors.keyValue}
                sx={{
                  "& .MuiInputBase-input": {
                    fontFamily: "monospace",
                  },
                }}
              />
            </FormControl>
          </Grid>
        </Grid>
      )}

      <Stack spacing={1.5} width="100%">
        <Collapse in={!!status && !isDirty} timeout={300}>
          {status && (
            <Alert
              severity={status.severity}
              onClose={() => setStatus(null)}
              sx={{ width: "100%", maxWidth: 480 }}
            >
              {status.message}
            </Alert>
          )}
        </Collapse>
        <Stack direction="row" spacing={1.5} justifyContent="flex-end">
          <Button
            variant="outlined"
            onClick={handleDiscard}
            disabled={!isDirty || isUpdating}
          >
            Discard
          </Button>
          <Button
            variant="contained"
            onClick={() => void handleSave()}
            disabled={isUpdating || !isDirty}
          >
            {isUpdating ? "Saving..." : "Save"}
          </Button>
        </Stack>
      </Stack>

      {orgName && proxyId && (
        <CreateScopeDrawer
          open={createScopeDrawerOpen}
          onClose={() => setCreateScopeDrawerOpen(false)}
          orgName={orgName}
          proxyId={proxyId}
          environments={environments}
          tools={toolEntries}
        />
      )}
    </Stack>
  );
}
