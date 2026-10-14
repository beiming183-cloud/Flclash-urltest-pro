import 'package:fl_clash/core/core.dart';
import 'package:fl_clash/core/interface.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/models/models.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:fl_clash/state.dart';
import 'package:fl_clash/views/dashboard/dashboard.dart';
import 'package:fl_clash/views/proxies/proxies.dart';
import 'package:fl_clash/views/stability.dart';
import 'package:fl_clash/widgets/widgets.dart';
import 'package:material_ui/material_ui.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/test_app.dart';
import '../helpers/test_profiles.dart';

class _RecordingAction extends ProxiesAction {
  static final tested = <String>[];
  static final selected = <(String, String)>[];

  @override
  Future<void> delayTestPageGroup(String groupName) async {
    tested.add(groupName);
  }

  @override
  void changeProxyDebounce(String groupName, String proxyName) {
    selected.add((groupName, proxyName));
  }
}

class _Handler extends CoreHandlerInterface {
  int stateReads = 0;
  int exports = 0;

  @override
  Future<List<dynamic>> getStabilityState() async {
    stateReads++;
    return [];
  }

  @override
  Future<Map<String, dynamic>?> exportStabilityDiagnostics() async {
    exports++;
    return {'base64': '', 'size': 1};
  }

  @override
  Future<T?> invokeMethod<T>({
    required CoreMethod method,
    Object? arguments,
    Duration? timeout,
  }) async => throw UnimplementedError();

  @override
  Future<CoreLifecycleResult> start() async => const CoreLifecycleResult(
    revision: 1,
    outcome: CoreLifecycleOutcome.applied,
  );

  @override
  Future<CoreLifecycleResult> restart() => start();

  @override
  Future<CoreLifecycleResult> stop() => start();

  @override
  Future<CoreLifecycleResult> close() => start();
}

void main() {
  late ProviderContainer container;
  late _Handler handler;

  Future<void> mount(WidgetTester tester, Widget view) async {
    tester.view.physicalSize = const Size(400, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    _RecordingAction.tested.clear();
    _RecordingAction.selected.clear();
    handler = _Handler();
    final profile = Profile.normal().copyWith(currentGroupName: 'IP1');
    const group = Group(
      name: 'IP1',
      type: GroupType.URLTest,
      now: 'Home A',
      all: [
        Proxy(name: 'Home A', type: 'Http'),
        Proxy(name: 'Home B', type: 'Socks5'),
      ],
    );
    container = ProviderContainer(
      overrides: [
        profilesProvider.overrideWith(() => TestProfiles([profile])),
        currentProfileIdProvider.overrideWithBuild((_, _) => profile.id),
        currentGroupsStateProvider.overrideWithValue(
          const GroupsState(value: [group]),
        ),
        groupsProvider.overrideWithValue(const [group]),
        proxiesActionProvider.overrideWith(_RecordingAction.new),
        coreHandlerProvider.overrideWithValue(CoreController.scoped(handler)),
      ],
    );
    addTearDown(container.dispose);
    globalState.container = container;
    final hold = container.listen(currentProfileProvider, (_, _) {});
    addTearDown(hold.close);
    container.read(coreStatusProvider.notifier).value = CoreStatus.connected;
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: TestApp(
          child: DockedPageScope(docked: true, child: view),
        ),
      ),
    );
    await tester.pump();
  }

  testWidgets('official dashboard has no injected stability panel', (tester) async {
    await mount(tester, const DashboardView());
    expect(find.byType(StabilityPanel), findsNothing);
    expect(handler.stateReads, 0);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('original one-tap delay test and card selection remain direct', (
    tester,
  ) async {
    await mount(tester, const ProxiesView());
    expect(find.byType(StabilityPanel), findsNothing);
    final delayButton = find.descendant(
      of: find.byType(AppBar),
      matching: find.byTooltip('Delay test'),
    );
    await tester.tap(delayButton);
    await tester.pump();
    expect(_RecordingAction.tested, ['IP1']);
    expect(handler.stateReads, 0);
    await tester.tap(find.text('Home B', findRichText: true));
    await tester.pump();
    expect(_RecordingAction.selected, [('IP1', 'Home B')]);
    expect(handler.exports, 0);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('new optional entries open stability without replacing proxies', (
    tester,
  ) async {
    await mount(tester, const ProxiesView());
    await tester.tap(find.byTooltip('More').first);
    await tester.pumpAndSettle();
    expect(find.text('Residential chain stability'), findsOneWidget);
    expect(find.text('Export diagnostics (≤2 MiB)'), findsOneWidget);
    await tester.tap(find.text('Residential chain stability'));
    await tester.pump(const Duration(seconds: 1));
    expect(find.byType(StabilityView), findsOneWidget);
    expect(handler.stateReads, greaterThan(0));
    expect(_RecordingAction.tested, isEmpty);
    expect(_RecordingAction.selected, isEmpty);
    await tester.pumpWidget(const SizedBox());
  });

  test('diagnostic shortcut rejects invalid archives before saving', () async {
    final core = CoreController.scoped(_Handler());
    await expectLater(exportStabilityDiagnostics(core), throwsStateError);
  });
}
