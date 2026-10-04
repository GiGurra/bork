# Driver serial-root audit

Snapshot: main `04e3964`, second uncached normal milestone run. The 188 passing
non-parallel roots account for 49.33 s before the parallel group starts at 50.25 s.
Target: serial phase below 15 s. The later collector refactor removed nine roots;
their rows remain here to explain the measured baseline. Newly added roots need
their own scheduling review and focused timings. Four inactive HTTP race parents
skip before scheduling and run in parallel in race builds; they are not serial
execution work.

The first slice parallelizes the 87 surviving roots whose owned fixtures and
transitive test helpers do not mutate the environment, working directory or
process hooks. Cleanup accounting uses a per-call small read batch for its
nested regression, with production batches unchanged at 256. Remaining roots
need per-call environment/hook options or must retain serialization when
exercising the ambient process interface itself.

| Root | Seconds | Decision | Dependency |
| --- | ---: | --- | --- |
| [TestExecutionObservedComptime](../../internal/driver/execution_observe_test.go) | 5.87 | Replace global dependency with per-call options | Setenv, global:goModuleHook |
| [TestCacheRemovalSupportsLargeTree](../../internal/driver/cache_clean_test.go) | 3.39 | Shrink with per-call read batch; rename to `TestCacheRemovalMultiBatchNestedAccounting` and parallelize | Owned fixtures; immutable inputs |
| TestExecutionGoNativeClosure | 2.03 | Retired by collector refactor | Setenv, global:goModuleHook |
| [TestFixtureOutputLifecycle](../../internal/driver/test_output_lifecycle_linux_test.go) | 1.68 | Replace global dependency with per-call options | Setenv |
| [TestComptimeRequiresNativeTarget](../../internal/driver/comptime_test.go) | 1.63 | Replace global dependency with per-call options | Setenv |
| [TestInterpolationValidatorLimitsAndDiagnostics](../../internal/driver/interpolation_validation_test.go) | 1.52 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestStandardGoDependencies](../../internal/driver/deps_test.go) | 1.14 | Replace global dependency with per-call options | Setenv |
| [TestUserGoDependencies](../../internal/driver/userdeps_test.go) | 1.14 | Replace global dependency with per-call options | Setenv |
| [TestInterpolationValidatorOutsideFunctions](../../internal/driver/interpolation_validation_test.go) | 1.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheValidationRejectsChanges](../../internal/driver/cache_validation_test.go) | 1.01 | Replace global dependency with per-call options | Setenv |
| [TestFixtureOutputFailureAndFallback](../../internal/driver/test_output_lifecycle_linux_test.go) | 1.00 | Replace global dependency with per-call options | Setenv |
| [TestCompilationFreezesGoManifests](../../internal/driver/compilation_inputs_test.go) | 0.98 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestImportedDefaultsRetainUseSiteFacts](../../internal/driver/gotypes_test.go) | 0.92 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestInterpolationValidatorBatchAndMemo](../../internal/driver/interpolation_validation_test.go) | 0.87 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheStoreConcurrentReplacement](../../internal/driver/cache_store_test.go) | 0.84 | Replace global dependency with per-call options | Setenv |
| [TestSessionBypassesEvaluationAndTypes](../../internal/driver/session_test.go) | 0.84 | Replace global dependency with per-call options | Setenv |
| [TestPredicateMemoModuleHook](../../internal/driver/predicate_memo_test.go) | 0.77 | Replace global dependency with per-call options | global:goModuleHook |
| [TestInterpolationValidatorOwnerAndMetadata](../../internal/driver/interpolation_validation_test.go) | 0.76 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSessionComptimeBypassesUntrackedInputs](../../internal/driver/comptime_test.go) | 0.72 | Replace global dependency with per-call options | Setenv |
| [TestSessionAddedInvalidDefaultAndRepair](../../internal/driver/session_test.go) | 0.71 | Replace global dependency with per-call options | Setenv |
| [TestImportedDefaultFactsNeedProofPerBuild](../../internal/driver/gotypes_test.go) | 0.70 | Replace global dependency with per-call options | Setenv |
| [TestInterpolationValidatorCompletedComptimeDependency](../../internal/driver/interpolation_validation_test.go) | 0.68 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheMissSkipsBypassInventories](../../internal/driver/cache_miss_test.go) | 0.61 | Replace global dependency with per-call options | Setenv |
| [TestGoContextRetainedForBuildAndPredicates](../../internal/driver/go_context_test.go) | 0.61 | Replace global dependency with per-call options | Setenv |
| [TestSessionMatchesClean](../../internal/driver/session_test.go) | 0.60 | Replace global dependency with per-call options | Setenv |
| [TestStandardInterpolationBudgetFallback](../../internal/driver/interpolation_native_test.go) | 0.59 | Replace global dependency with per-call options | global:goModuleHook |
| [TestHTTPDeclaredDefaultFacts](../../internal/driver/gotypes_test.go) | 0.58 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestPreludeDiet](../../internal/driver/prelude_diet_test.go) | 0.51 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestDepsHelper](../../internal/driver/deps_helper_test.go) | 0.50 | Replace global dependency with per-call options | Setenv |
| [TestCacheArtifactCompositeCertification](../../internal/driver/cache_artifact_test.go) | 0.45 | Replace global dependency with per-call options | Setenv |
| [TestGoReceiptRejectsChangedInputs](../../internal/driver/cache_go_receipt_test.go) | 0.44 | Replace global dependency with per-call options | Chdir, Setenv |
| [TestSessionEmitRequiresMain](../../internal/driver/session_main_test.go) | 0.40 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheArtifactEncodingBudgets](../../internal/driver/cache_artifact_test.go) | 0.38 | Replace global dependency with per-call options | Setenv |
| [TestCacheArtifactRejectsCorruption](../../internal/driver/cache_artifact_test.go) | 0.37 | Replace global dependency with per-call options | Setenv |
| [TestCacheResultPublicationDoesNotScanOrEvict](../../internal/driver/cache_lifecycle_test.go) | 0.35 | Replace global dependency with per-call options | Setenv |
| [TestSessionConfigurationAndModeChanges](../../internal/driver/session_test.go) | 0.35 | Replace global dependency with per-call options | Setenv |
| [TestWatchBypassDoesNotRecompileWhilePolling](../../internal/driver/watch_test.go) | 0.34 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheFindSkipsStaleNamespaces](../../internal/driver/cache_store_test.go) | 0.33 | Replace global dependency with per-call options | Setenv |
| [TestSessionRefreshesStandardPackageNames](../../internal/driver/session_test.go) | 0.33 | Replace global dependency with per-call options | Setenv |
| [TestSessionAssetChanges](../../internal/driver/session_test.go) | 0.31 | Replace global dependency with per-call options | Setenv |
| [TestCacheArtifactChecksumCoversReceipt](../../internal/driver/cache_artifact_test.go) | 0.30 | Replace global dependency with per-call options | Setenv |
| [TestCacheTrimResultNamespacesAndLocators](../../internal/driver/cache_trim_test.go) | 0.29 | Replace global dependency with per-call options | Setenv |
| [TestCacheArtifactRoundtrip](../../internal/driver/cache_artifact_test.go) | 0.28 | Replace global dependency with per-call options | Setenv |
| [TestCacheMissDeclinesChangesDuringLateCapture](../../internal/driver/cache_miss_test.go) | 0.28 | Replace global dependency with per-call options | Setenv |
| [TestCacheMissOtherBypassesSkipInventories](../../internal/driver/cache_miss_test.go) | 0.28 | Replace global dependency with per-call options | Setenv |
| [TestSessionEmitHitAndSourceEdit](../../internal/driver/session_test.go) | 0.28 | Replace global dependency with per-call options | Setenv |
| [TestWatchAttemptRetainsBypassedInputs](../../internal/driver/watch_test.go) | 0.28 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheValidationRejectsLauncherChangedAfterStartup](../../internal/driver/cache_validation_test.go) | 0.27 | Replace global dependency with per-call options | Setenv |
| [TestGoStageFallbackBuildsFresh](../../internal/driver/go_stage_test.go) | 0.27 | Replace global dependency with per-call options | Setenv |
| [TestCacheStoreCrossProcess](../../internal/driver/cache_store_test.go) | 0.25 | Replace global dependency with per-call options | Setenv |
| [TestCacheValidationMetadataInventory](../../internal/driver/cache_validation_test.go) | 0.25 | Replace global dependency with per-call options | Setenv |
| [TestWatchMissingAssetRecovers](../../internal/driver/watch_test.go) | 0.25 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheResultLocatorAlternatingCompilers](../../internal/driver/cache_lifecycle_test.go) | 0.23 | Replace global dependency with per-call options | Setenv |
| [TestSessionGoContextContentValidation](../../internal/driver/go_validation_test.go) | 0.23 | Replace global dependency with per-call options | Setenv |
| [TestCacheCleanNamespacesVersionsAndPreservation](../../internal/driver/cache_clean_test.go) | 0.22 | Replace global dependency with per-call options | Setenv |
| [TestCacheResultHourlyUse](../../internal/driver/cache_lifecycle_test.go) | 0.21 | Replace global dependency with per-call options | Setenv |
| [TestCacheResultLocatorFailurePreservesArtifact](../../internal/driver/cache_lifecycle_test.go) | 0.21 | Replace global dependency with per-call options | Setenv |
| [TestCacheStoreRoundtripAndMisses](../../internal/driver/cache_store_test.go) | 0.21 | Replace global dependency with per-call options | Setenv |
| [TestSessionConcurrentRequests](../../internal/driver/session_test.go) | 0.21 | Replace global dependency with per-call options | Setenv |
| [TestSessionGoContextTelemetrySettings](../../internal/driver/go_validation_test.go) | 0.21 | Replace global dependency with per-call options | Setenv |
| [TestWatchPendingManualRequestWithholdsOldResult](../../internal/driver/watch_test.go) | 0.21 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestWatchRecoversAndTracksEqualMtimeEdits](../../internal/driver/watch_test.go) | 0.21 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheArtifactDeclinesAssetsAndLossyStrings](../../internal/driver/cache_artifact_test.go) | 0.20 | Replace global dependency with per-call options | Setenv |
| [TestCacheValidationOwnedResult](../../internal/driver/cache_validation_test.go) | 0.20 | Replace global dependency with per-call options | Setenv |
| [TestWatchManualRetrigger](../../internal/driver/watch_test.go) | 0.20 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSessionGoContextNegativeModuleInputs](../../internal/driver/go_validation_test.go) | 0.19 | Replace global dependency with per-call options | Chdir, Setenv |
| [TestCacheMissEligibleArtifact](../../internal/driver/cache_miss_test.go) | 0.17 | Replace global dependency with per-call options | Setenv |
| [TestCacheResultTouch](../../internal/driver/cache_lifecycle_test.go) | 0.17 | Replace global dependency with per-call options | Setenv |
| [TestCacheStoreDeclinesSymlinkEscape](../../internal/driver/cache_store_test.go) | 0.17 | Replace global dependency with per-call options | Setenv |
| [TestStableGoStageConcurrentProcesses](../../internal/driver/go_stage_test.go) | 0.17 | Replace global dependency with per-call options | Setenv |
| [TestWatchMissingRootAppears](../../internal/driver/watch_test.go) | 0.17 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheInstalledSDKHitStillValidatesSources](../../internal/driver/cache_installed_sdk_test.go) | 0.16 | Replace global dependency with per-call options | Setenv |
| [TestGoStagePublicationDoesNotScanOrEvict](../../internal/driver/go_stage_lifecycle_test.go) | 0.16 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestStableStageMappedSourceLocations](../../internal/driver/go_stage_locations_test.go) | 0.16 | Replace global dependency with per-call options | Setenv |
| [TestSessionCheckWarningsOwned](../../internal/driver/session_test.go) | 0.15 | Replace global dependency with per-call options | Setenv |
| [TestGoReceiptLauncherEqualMtimeEdit](../../internal/driver/cache_go_receipt_test.go) | 0.14 | Replace global dependency with per-call options | Setenv |
| [TestSessionGoContextLauncherRelocation](../../internal/driver/go_validation_test.go) | 0.14 | Replace global dependency with per-call options | Setenv |
| [TestStandardNameInputsKeepMetadataDriverEvidence](../../internal/driver/go_validation_test.go) | 0.14 | Replace global dependency with per-call options | Setenv |
| [TestSessionGoContextInvalidCgoSetting](../../internal/driver/go_validation_test.go) | 0.12 | Replace global dependency with per-call options | Setenv |
| [TestSessionGoContextSDKSettings](../../internal/driver/go_validation_test.go) | 0.12 | Replace global dependency with per-call options | Setenv |
| [TestWatchIncludesMigrationWarning](../../internal/driver/watch_test.go) | 0.12 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoNamesGOPATH](../../internal/driver/go_names_test.go) | 0.11 | Replace global dependency with per-call options | Setenv |
| [TestSessionGoContextTemporaryDirectory](../../internal/driver/go_validation_test.go) | 0.11 | Replace global dependency with per-call options | Setenv |
| [TestGoContextExternalDriverEnvironmentAndFallback](../../internal/driver/go_context_test.go) | 0.10 | Replace global dependency with per-call options | Setenv |
| [TestGoNameReceiptDetectsSDKChanges](../../internal/driver/cache_go_receipt_test.go) | 0.10 | Replace global dependency with per-call options | Setenv |
| [TestGoNamesExternalDriver](../../internal/driver/go_names_test.go) | 0.10 | Replace global dependency with per-call options | Setenv |
| [TestDescribeOk](../../internal/driver/migration_test.go) | 0.09 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoContextMetadataUsesCapturedLauncher](../../internal/driver/go_context_test.go) | 0.09 | Replace global dependency with per-call options | Setenv |
| [TestGoNameReceiptRoundtrip](../../internal/driver/cache_go_receipt_test.go) | 0.09 | Replace global dependency with per-call options | Setenv |
| [TestGoReceiptRoundtrip](../../internal/driver/cache_go_receipt_test.go) | 0.09 | Replace global dependency with per-call options | Setenv |
| [TestInterpolationValidatorSelectionErrors](../../internal/driver/interpolation_validation_test.go) | 0.08 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestStandardInterpolationArtifactBinding](../../internal/driver/interpolation_native_test.go) | 0.08 | Replace global dependency with per-call options | global:goModuleHook |
| [TestCachePublishJobOwnsInputsAndRejectsChanges](../../internal/driver/cache_publish_job_test.go) | 0.07 | Replace global dependency with per-call options | Setenv |
| [TestCacheTrimMissingMarkerAndInvalidProgress](../../internal/driver/cache_trim_test.go) | 0.07 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheTrimShardProgressAndDailyGate](../../internal/driver/cache_trim_test.go) | 0.06 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoContextFreezesImplicitDriverChoice](../../internal/driver/go_context_test.go) | 0.06 | Replace global dependency with per-call options | Setenv |
| [TestObservedEmission](../../internal/driver/performance_test.go) | 0.06 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSessionGoContextFIPSBypass](../../internal/driver/go_validation_test.go) | 0.06 | Replace global dependency with per-call options | Setenv |
| [TestCompilerIdentityRunningInode](../../internal/driver/cache_compiler_identity_test.go) | 0.05 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoContextFreezesSavedSettings](../../internal/driver/go_context_test.go) | 0.05 | Replace global dependency with per-call options | Setenv |
| [TestCacheTrimBusyEntryAndLockAlias](../../internal/driver/cache_trim_test.go) | 0.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCompilationRetriesCombinedCapture](../../internal/driver/compilation_inputs_test.go) | 0.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| TestExecutionGoNestedAssemblyHeaders | 0.04 | Retired by collector refactor | Owned fixtures; immutable inputs |
| [TestExecutionReceiptBoundsMutatedMetadataBeforeHashing](../../internal/driver/execution_identity_test.go) | 0.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoContextExternalNamesRemainFresh](../../internal/driver/go_context_test.go) | 0.04 | Replace global dependency with per-call options | Setenv, global:goModuleHook |
| [TestGoStageFixedPendingAndMetadata](../../internal/driver/go_stage_lifecycle_test.go) | 0.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoToolIdentityInvalidatesChanges](../../internal/driver/go_tool_identity_test.go) | 0.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestInterpolationWithoutValidatorHasNoEvaluator](../../internal/driver/interpolation_validation_test.go) | 0.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSourceSnapshotFrozenReads](../../internal/driver/source_inputs_test.go) | 0.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestStableGoStageReplacesCompleteInputs](../../internal/driver/go_stage_test.go) | 0.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestUnitMigrationFixes](../../internal/driver/migration_test.go) | 0.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestUserGoManifestErrors](../../internal/driver/userdeps_test.go) | 0.04 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheCleanDirectoryBatchesCancel](../../internal/driver/cache_clean_test.go) | 0.03 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheCleanDrainsPublisherAdmission](../../internal/driver/cache_clean_test.go) | 0.03 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheCleanWaitsForStageAndCancellation](../../internal/driver/cache_clean_test.go) | 0.03 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheTrimDetachedEntryRepublicationAndTrashRecovery](../../internal/driver/cache_trim_test.go) | 0.03 | Retain serial execution | Immediate nonblocking lock reacquisition; concurrent launch dependency |
| [TestDriverStageCacheRoot](../../internal/driver/stage_cache_linux_test.go) | 0.03 | Replace global dependency with per-call options | Setenv |
| [TestEmbedInputsFrozenBytes](../../internal/driver/embed_inputs_test.go) | 0.03 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestExecutionCandidatePriorReservesMetadataBudget](../../internal/driver/execution_identity_test.go) | 0.03 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSessionGoContextUnknownLauncher](../../internal/driver/go_validation_test.go) | 0.03 | Replace global dependency with per-call options | Setenv |
| [TestStandardInterpolationAvoidsGoBuild](../../internal/driver/interpolation_native_test.go) | 0.03 | Replace global dependency with per-call options | global:goModuleHook |
| [TestCompilationInventoriesAbsentManifest](../../internal/driver/compilation_inputs_test.go) | 0.02 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCompilationRetainsAssetInventory](../../internal/driver/embed_inputs_test.go) | 0.02 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCompilerArtifactNamespace](../../internal/driver/cache_compiler_identity_test.go) | 0.02 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoBindingsReject32BitTarget](../../internal/driver/gotypes_test.go) | 0.02 | Replace global dependency with per-call options | Setenv |
| [TestGoContextCapturesGoDespiteMissingDriver](../../internal/driver/go_context_test.go) | 0.02 | Replace global dependency with per-call options | Setenv |
| [TestGoContextLauncherShimPreservesAuxiliaryPATH](../../internal/driver/go_context_test.go) | 0.02 | Replace global dependency with per-call options | Setenv |
| [TestGoContextMetadataEnvironmentIsolation](../../internal/driver/go_context_test.go) | 0.02 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoContextMissingGoRemainsDeferred](../../internal/driver/go_context_test.go) | 0.02 | Replace global dependency with per-call options | Setenv |
| [TestInterpolationValidatorPurity](../../internal/driver/interpolation_validation_test.go) | 0.02 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestUnitVariantIsNotDeprecated](../../internal/driver/migration_test.go) | 0.02 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheCleanRejectsAliasedLayersAndLocks](../../internal/driver/cache_clean_test.go) | 0.01 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCheckLeavesSyntax](../../internal/driver/syntax_test.go) | 0.01 | Parallelize isolated root | Owned fixtures; immutable inputs |
| TestExecutionGoAssemblyCommentAdjacentIncludes | 0.01 | Retired by collector refactor | Owned fixtures; immutable inputs |
| [TestExecutionIdentityOwnershipAndBoundaries](../../internal/driver/execution_identity_test.go) | 0.01 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoStageHourlyUseSurvivesReplacement](../../internal/driver/go_stage_lifecycle_test.go) | 0.01 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestHTTPTypeCheckWithoutGo](../../internal/driver/gotypes_test.go) | 0.01 | Replace global dependency with per-call options | Setenv |
| [TestShippedGoBindingsRequireSignatures](../../internal/driver/gotypes_test.go) | 0.01 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSourceSnapshotConcurrentReplay](../../internal/driver/source_inputs_test.go) | 0.01 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSourceSnapshotReplaysLoad](../../internal/driver/source_inputs_test.go) | 0.01 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheCleanLocatorTemporaries](../../internal/driver/cache_clean_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheCleanMissingUnavailableAndSymlink](../../internal/driver/cache_clean_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheInstalledSDKReplacementInvalidates](../../internal/driver/cache_installed_sdk_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheStoreLockPoolBounded](../../internal/driver/cache_store_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCacheTrimCancellationAndCleanTrash](../../internal/driver/cache_trim_test.go) | 0.00 | Retain serial execution | Immediate nonblocking lock reacquisition; concurrent launch dependency |
| [TestCacheTrimSchedulingGate](../../internal/driver/cache_trim_schedule_unix_test.go) | 0.00 | Replace global dependency with per-call options | Setenv |
| [TestCacheUseDeclinesAliases](../../internal/driver/cache_lifecycle_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestCompilationReportsSyntaxBeforeManifest](../../internal/driver/compilation_inputs_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestConfiguredCacheRoot](../../internal/driver/cache_root_test.go) | 0.00 | Replace global dependency with per-call options | Setenv |
| [TestDepsHelperErrors](../../internal/driver/deps_helper_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestDriverStageCacheRejectsUnsafeRoot](../../internal/driver/stage_cache_linux_test.go) | 0.00 | Replace global dependency with per-call options | Setenv |
| [TestEmbedInputsConcurrentReplay](../../internal/driver/embed_inputs_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestEmbedInputsDirectoryMembership](../../internal/driver/embed_inputs_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestEmbedInputsInventoryInvalidUTF8](../../internal/driver/embed_inputs_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestEmbedInputsNegativeAndSymlinkReads](../../internal/driver/embed_inputs_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestEmbedInputsPreserveErrorPaths](../../internal/driver/embed_inputs_test.go) | 0.00 | Replace global dependency with per-call options | Chdir |
| [TestEmbedInputsRetryDuringCapture](../../internal/driver/embed_inputs_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestExecutionFoundationDeclinesUncertifiedClosure](../../internal/driver/execution_identity_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| TestExecutionGoAssemblyNegativeSearch | 0.00 | Retired by collector refactor | Owned fixtures; immutable inputs |
| TestExecutionGoContentAndMembership | 0.00 | Retired by collector refactor | Owned fixtures; immutable inputs |
| TestExecutionGoDiscoveryPolicy | 0.00 | Retired by collector refactor | Owned fixtures; immutable inputs |
| TestExecutionGoFIFOReplacement | 0.00 | Retired by collector refactor | Owned fixtures; immutable inputs |
| TestExecutionGoInputBudgetsAndOwnership | 0.00 | Retired by collector refactor | Owned fixtures; immutable inputs |
| TestExecutionObservationInvalidEndpointReleasesStage | 0.00 | Retired by collector refactor | Owned fixtures; immutable inputs |
| [TestExecutionPriorResultBindingAndOrder](../../internal/driver/execution_identity_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestExecutionTrackerOwnsExpectedInputAndBoundsInvocations](../../internal/driver/execution_tracker_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestExecutionTrackerRejectsForeignDuplicateUnboundAndMismatched](../../internal/driver/execution_tracker_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestFixtureDirectoryLifecycle](../../internal/driver/fixture_dir_linux_test.go) | 0.00 | Replace global dependency with per-call options | Setenv |
| [TestFixtureOutputRejectsAliases](../../internal/driver/test_output_lifecycle_linux_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestFrozenGoModuleStaging](../../internal/driver/compilation_inputs_test.go) | 0.00 | Replace global dependency with per-call options | global:goModuleHook |
| [TestGoStageDeclinesContainedEntryAlias](../../internal/driver/go_stage_lifecycle_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoStageLockPoolBounded](../../internal/driver/go_stage_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoToolDigestFallbackDetectsEqualMtimeEdit](../../internal/driver/go_tool_identity_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoToolIdentityFollowsSymlinkTarget](../../internal/driver/go_tool_identity_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoToolIdentityRehashesRacyMetadataMatch](../../internal/driver/go_tool_identity_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestGoToolIdentityStableObservation](../../internal/driver/go_tool_identity_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestMetadataContentHashRejectsSpecialFiles](../../internal/driver/go_validation_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestParseModFile](../../internal/driver/modfile_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSourceReceiptContentMembershipAndMissing](../../internal/driver/cache_source_receipt_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSourceReceiptInvalidUTF8Declines](../../internal/driver/cache_source_receipt_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSourceReceiptRawPaths](../../internal/driver/cache_source_receipt_test.go) | 0.00 | Replace global dependency with per-call options | Chdir |
| [TestSourceReceiptReplaysLoader](../../internal/driver/cache_source_receipt_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSourceReceiptUnsupported](../../internal/driver/cache_source_receipt_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSourceSnapshotErrorPaths](../../internal/driver/source_inputs_test.go) | 0.00 | Replace global dependency with per-call options | Chdir |
| [TestSourceSnapshotMissingModuleAndMembership](../../internal/driver/source_inputs_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSourceSnapshotPreservesIOPaths](../../internal/driver/source_inputs_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestSourceSnapshotRetriesDuringCapture](../../internal/driver/source_inputs_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |
| [TestWatchCancellationDuringValidationDoesNotPublish](../../internal/driver/watch_test.go) | 0.00 | Parallelize isolated root | Owned fixtures; immutable inputs |

Two close-sensitive trim fixtures retain serial execution: focused parallel
repetition exposed transient `EAGAIN` on immediate nonblocking reacquisition.
Descriptor inheritance is a plausible cause: [flock locks belong to open file
descriptions shared by forked descriptors](https://man7.org/linux/man-pages/man2/flock.2.html)
and survive until every reference closes. Production busy-lock declines remain
unchanged; the tests do not retry or hide admission failures.
