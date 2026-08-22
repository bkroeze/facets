import Quickshell
import Quickshell.Io
import Quickshell.Wayland
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    id: shell
    // Quattro injects these properties when it mounts a plugin. Facets does
    // not depend on shell internals, but declaring the contract keeps the
    // panel compatible with the host's loader and future shared services.
    property string omarchyPath: ""
    property var manifest: null
    property var pluginRegistry: null

    property bool panelVisible: false
    readonly property bool opened: panelVisible
    property bool loading: true
    property bool disconnected: false
    property string refreshError: ""
    property string parseError: ""
    property string daemonStderr: ""
    property string launchError: ""
    property string launchNotice: ""
    property string launchingProjectId: ""
    property var pendingProject: null
    property var resolvedTools: []
    property var projects: []
    property var expandedProjects: ({})
    property int restartAttempt: 0

    // Action-dialog tokens extend the panel's existing compact dark palette.
    readonly property color actionSurface: "#171c25"
    readonly property color actionSurfaceRaised: "#202733"
    readonly property color actionBorder: "#3a4352"
    readonly property color actionAccent: "#6f8cff"
    readonly property color actionDanger: "#d97782"
    readonly property color actionText: "#edf0f5"
    readonly property color actionMutedText: "#8993a4"
    readonly property color actionSuccess: "#8fc9a3"
    readonly property color actionErrorSurface: "#3a2429"
    readonly property color actionAccentMuted: "#52699f"
    readonly property color actionScrim: "#990b0e14"
    readonly property color actionErrorBorder: "#70404a"
    readonly property color actionErrorText: "#f4d6a0"
    readonly property int space1: 4
    readonly property int space2: 8
    readonly property int space3: 12
    readonly property int space4: 16
    readonly property int controlRadius: 8

    property string taskActionKind: ""
    property string taskActionProjectId: ""
    property string taskActionId: ""
    property string taskActionTitle: ""
    property bool taskActionBusy: false
    property string taskActionStdout: ""
    property string taskActionStderr: ""
    property string taskActionError: ""
    property string taskActionNotice: ""
    property bool taskActionTopValue: false

    function isTask(value) {
        return value !== null
            && typeof value === "object"
            && typeof value.id === "string"
            && typeof value.title === "string"
            && typeof value.status === "string"
            && (value.priority === null || typeof value.priority === "number")
            && typeof value.assignee === "string"
            && typeof value.updated_at === "string"
            && typeof value.top === "boolean";
    }

    function isProject(value) {
        if (value === null
                || typeof value !== "object"
                || typeof value.id !== "string"
                || typeof value.name !== "string"
                || typeof value.directory !== "string"
                || !Array.isArray(value.tasks))
            return false;

        for (let index = 0; index < value.tasks.length; ++index) {
            if (!isTask(value.tasks[index]))
                return false;
        }
        return true;
    }

    function acceptSnapshot(event) {
        if (!Array.isArray(event.projects))
            return false;
        for (let index = 0; index < event.projects.length; ++index) {
            if (!isProject(event.projects[index]))
                return false;
        }

        projects = event.projects;
        loading = false;
        disconnected = false;
        refreshError = "";
        parseError = "";
        restartAttempt = 0;
        return true;
    }

    function handleDaemonLine(line) {
        const text = line.trim();
        if (text.length === 0)
            return;

        let event;
        try {
            event = JSON.parse(text);
        } catch (error) {
            loading = false;
            parseError = "Malformed daemon output: " + error;
            return;
        }

        if (event === null || typeof event !== "object" || typeof event.type !== "string") {
            loading = false;
            parseError = "Malformed daemon event: missing type";
            return;
        }
        if (event.type === "snapshot") {
            if (!acceptSnapshot(event)) {
                loading = false;
                parseError = "Malformed snapshot: expected complete project and task fields";
            }
            return;
        }
        if (event.type === "error" && typeof event.message === "string") {
            loading = false;
            refreshError = event.message;
            return;
        }

        loading = false;
        parseError = "Unsupported daemon event: " + event.type;
    }

    function handleDaemonStderr(line) {
        const text = line.trim();
        if (text.length === 0)
            return;
        daemonStderr = text;
        console.error("facets task daemon: " + text);
    }

    function isExpanded(projectId) {
        return expandedProjects[projectId] === true;
    }

    function toggleProject(projectId) {
        const next = Object.assign({}, expandedProjects);
        next[projectId] = !isExpanded(projectId);
        expandedProjects = next;
    }

    function launchProject(project) {
        if (launchingProjectId !== "")
            return;
        launchNotice = "";
        launchError = "";
        if (project.directory === "") {
            launchError = "Set a working directory first: facets projects set "
                + project.id + " directory=<path>";
            return;
        }

        pendingProject = project;
        launchingProjectId = project.id;
        directoryProbe.command = ["/usr/bin/test", "-d", project.directory];
        directoryProbe.running = true;
    }

    function openTaskAction(kind, projectId, task) {
        if (taskActionBusy || taskActionDialog.opened)
            return;

        taskActionKind = kind;
        taskActionProjectId = projectId;
        taskActionId = task.id;
        taskActionTitle = task.title;
        taskActionError = "";
        taskActionNotice = "";
        taskActionText.text = "";
        taskActionDialog.open();
    }

    function toggleTopTask(projectId, task) {
        if (taskActionBusy || taskActionDialog.opened)
            return;

        taskActionKind = "top";
        taskActionProjectId = projectId;
        taskActionId = task.id;
        taskActionTitle = task.title;
        taskActionTopValue = !task.top;
        taskActionError = "";
        taskActionNotice = "";
        taskActionStdout = "";
        taskActionStderr = "";
        taskActionBusy = true;
        taskActionProcess.command = [
            "facets",
            "--project", projectId,
            "--format", "json",
            "tasks", "top", task.id,
            "--set", taskActionTopValue ? "true" : "false"
        ];
        taskActionProcess.running = true;
    }

    function actionFailureMessage(exitCode, exitStatus) {
        const output = taskActionStdout.trim();
        if (output !== "") {
            try {
                const document = JSON.parse(output);
                if (document !== null
                        && typeof document === "object"
                        && document.error !== null
                        && typeof document.error === "object"
                        && typeof document.error.message === "string"
                        && document.error.message.trim() !== "")
                    return document.error.message.trim();
                if (document !== null
                        && typeof document === "object"
                        && typeof document.message === "string"
                        && document.message.trim() !== "")
                    return document.message.trim();
            } catch (error) {
                // JSON command output is intentionally not shown verbatim in the panel.
            }
        }

        const stderrLines = taskActionStderr.split("\n");
        for (let index = 0; index < stderrLines.length; ++index) {
            const line = stderrLines[index].trim();
            if (line !== "" && !line.startsWith("{") && !line.startsWith("["))
                return line;
        }
        if (exitStatus !== 0)
            return "The facets process ended unexpectedly.";
        return "facets could not complete the action (exit code " + exitCode + ").";
    }

    function submitTaskAction() {
        if (taskActionBusy)
            return;

        const comment = taskActionText.text.trim();
        if (taskActionKind === "comment" && comment === "") {
            taskActionError = "Enter a comment before submitting.";
            taskActionText.forceActiveFocus();
            return;
        }

        let command = [
            "facets",
            "--project", taskActionProjectId,
            "--format", "json",
            "tasks"
        ];
        if (taskActionKind === "comment") {
            command = command.concat(["comment", taskActionId, "--body", comment]);
        } else {
            command = command.concat([
                "close", taskActionId,
                "--message", "Closed manually from the Facets QuickShell task panel",
                "--evidence", "test:manual confirmation in Facets QuickShell"
            ]);
            if (comment !== "")
                command = command.concat(["--comment", comment]);
        }

        taskActionError = "";
        taskActionNotice = "";
        taskActionStdout = "";
        taskActionStderr = "";
        taskActionBusy = true;
        taskActionProcess.command = command;
        taskActionProcess.running = true;
    }

    // Omarchy Quattro mounts this Item as a panel plugin. The host calls
    // these lifecycle methods when `omarchy-shell shell summon|hide|toggle`
    // addresses the plugin.
    function open(payloadJson) {
        shell.panelVisible = true;
    }

    function close() {
        shell.panelVisible = false;
    }

    IpcHandler {
        target: "facets"

        function toggle(): void {
            shell.panelVisible = !shell.panelVisible;
        }

        function show(): void {
            shell.panelVisible = true;
        }

        function hide(): void {
            shell.panelVisible = false;
        }

    }

    Process {
        id: daemon
        command: ["facets", "tasks", "daemon"]

        stdout: SplitParser {
            onRead: data => shell.handleDaemonLine(data)
        }

        stderr: SplitParser {
            onRead: data => shell.handleDaemonStderr(data)
        }

        onStarted: {
            shell.disconnected = false;
            shell.daemonStderr = "";
        }

        onExited: (exitCode, exitStatus) => {
            shell.loading = false;
            shell.disconnected = true;
            shell.restartAttempt = Math.min(shell.restartAttempt + 1, 4);
            restartTimer.interval = Math.min(10000, 1000 * Math.pow(2, shell.restartAttempt - 1));
            restartTimer.restart();
        }
    }

    Timer {
        id: restartTimer
        interval: 1000
        repeat: false
        onTriggered: daemon.running = true
    }

    Process {
        id: directoryProbe

        onExited: (exitCode, exitStatus) => {
            if (exitCode !== 0) {
                shell.launchError = "Configured directory does not exist: " + shell.pendingProject.directory;
                shell.launchingProjectId = "";
                shell.pendingProject = null;
                return;
            }
            shell.resolvedTools = [];
            toolResolver.running = true;
        }
    }

    Process {
        id: toolResolver
        command: ["/usr/bin/which", "uwsm-app", "xdg-terminal-exec", "kata"]

        stdout: SplitParser {
            onRead: data => {
                const path = data.trim();
                if (path !== "")
                    shell.resolvedTools = shell.resolvedTools.concat([path]);
            }
        }

        onExited: (exitCode, exitStatus) => {
            if (exitCode !== 0 || shell.resolvedTools.length !== 3) {
                shell.launchError = "Launch requires uwsm-app, xdg-terminal-exec, and kata in the graphical session PATH.";
                shell.launchingProjectId = "";
                shell.pendingProject = null;
                return;
            }

            const project = shell.pendingProject;
            terminalLauncher.command = [
                shell.resolvedTools[0],
                "-a", "facets-kata",
                "-d", "Facets Kata TUI - " + project.name,
                "--",
                shell.resolvedTools[1],
                "--app-id=TUI.float",
                "--title=Facets Kata - " + project.name,
                "--dir=" + project.directory,
                "--",
                shell.resolvedTools[2], "tui"
            ];
            terminalLauncher.startDetached();
            shell.launchNotice = "Opened Kata TUI for " + project.name;
            launchSettledTimer.restart();
        }
    }

    Process {
        id: terminalLauncher
    }

    Timer {
        id: launchSettledTimer
        interval: 1200
        repeat: false
        onTriggered: {
            shell.launchingProjectId = "";
            shell.pendingProject = null;
        }
    }

    Process {
        id: taskActionProcess

        stdout: SplitParser {
            onRead: data => shell.taskActionStdout += data + "\n"
        }

        stderr: SplitParser {
            onRead: data => shell.taskActionStderr += data + "\n"
        }

        onExited: (exitCode, exitStatus) => {
            shell.taskActionBusy = false;
            if (exitCode !== 0 || exitStatus !== 0) {
                shell.taskActionError = shell.actionFailureMessage(exitCode, exitStatus);
                return;
            }

            if (shell.taskActionKind === "top") {
                shell.taskActionNotice = shell.taskActionTopValue
                    ? "Marked " + shell.taskActionId + " as a top task."
                    : "Removed " + shell.taskActionId + " from top tasks.";
            } else {
                shell.taskActionNotice = shell.taskActionKind === "comment"
                    ? "Comment added to " + shell.taskActionId + "."
                    : "Closed " + shell.taskActionId + ".";
                taskActionDialog.close();
            }
            shell.taskActionError = "";
        }
    }

    Component.onCompleted: daemon.running = true

    PanelWindow {
        id: panel
        visible: shell.panelVisible
        color: "transparent"
        implicitWidth: 440
        implicitHeight: Math.min(720, screen ? screen.height - 28 : 720)
        exclusiveZone: 0
        exclusionMode: ExclusionMode.Ignore

        anchors {
            top: true
            right: true
        }

        margins {
            top: 14
            right: 14
        }

        WlrLayershell.namespace: "facets"
        WlrLayershell.layer: WlrLayer.Overlay
        WlrLayershell.keyboardFocus: WlrKeyboardFocus.OnDemand

        Rectangle {
            anchors.fill: parent
            color: "#11151d"
            radius: 14
            border.width: 1
            border.color: "#3a4352"

            Keys.onEscapePressed: {
                if (!shell.taskActionBusy && !taskActionDialog.opened)
                    shell.panelVisible = false;
            }

            ColumnLayout {
                id: panelColumn
                anchors.fill: parent
                anchors.margins: 16
                spacing: 12

                RowLayout {
                    Layout.fillWidth: true
                    spacing: 10

                    Rectangle {
                        Layout.preferredWidth: 30
                        Layout.preferredHeight: 30
                        radius: 8
                        color: "#6f8cff"

                        Text {
                            anchors.centerIn: parent
                            text: "F"
                            color: "#ffffff"
                            font.pixelSize: 15
                            font.weight: Font.DemiBold
                        }
                    }

                    ColumnLayout {
                        Layout.fillWidth: true
                        spacing: 1

                        Text {
                            text: "Facets"
                            color: "#f5f7fb"
                            font.pixelSize: 17
                            font.weight: Font.DemiBold
                        }

                        Text {
                            text: shell.loading
                                ? "Loading projects"
                                : shell.projects.length + (shell.projects.length === 1 ? " active project" : " active projects")
                            color: "#8993a4"
                            font.pixelSize: 11
                        }
                    }

                    Button {
                        id: closeButton
                        Accessible.name: "Close Facets panel"
                        Layout.preferredWidth: 30
                        Layout.preferredHeight: 30
                        text: "×"
                        flat: true
                        onClicked: shell.panelVisible = false

                        contentItem: Text {
                            text: closeButton.text
                            color: closeButton.hovered ? "#ffffff" : "#9ba5b5"
                            font.pixelSize: 19
                            horizontalAlignment: Text.AlignHCenter
                            verticalAlignment: Text.AlignVCenter
                        }

                        background: Rectangle {
                            radius: 7
                            color: closeButton.hovered ? "#29313e" : "transparent"
                        }
                    }
                }

                Rectangle {
                    visible: shell.taskActionError !== "" || shell.launchError !== "" || shell.refreshError !== "" || shell.parseError !== "" || shell.disconnected
                    Layout.fillWidth: true
                    implicitHeight: statusColumn.implicitHeight + 18
                    radius: 9
                    color: shell.taskActionError !== "" || shell.launchError !== "" || shell.parseError !== "" ? "#3a2429" : "#302b20"
                    border.width: 1
                    border.color: shell.taskActionError !== "" || shell.launchError !== "" || shell.parseError !== "" ? "#70404a" : "#66583a"

                    ColumnLayout {
                        id: statusColumn
                        anchors.fill: parent
                        anchors.margins: 9
                        spacing: 3

                        Text {
                            Layout.fillWidth: true
                            text: shell.taskActionError !== ""
                                ? shell.taskActionError
                                : shell.launchError !== ""
                                    ? shell.launchError
                                    : shell.parseError !== ""
                                        ? shell.parseError
                                        : shell.refreshError !== ""
                                            ? "Refresh failed: " + shell.refreshError
                                            : "Task daemon disconnected; retrying"
                            color: "#f4d6a0"
                            font.pixelSize: 11
                            wrapMode: Text.Wrap
                        }

                        Text {
                            visible: shell.daemonStderr !== ""
                            Layout.fillWidth: true
                            text: shell.daemonStderr
                            color: "#aeb6c4"
                            font.pixelSize: 10
                            elide: Text.ElideRight
                        }
                    }
                }

                Text {
                    visible: shell.taskActionNotice !== "" || shell.launchNotice !== ""
                    Layout.fillWidth: true
                    text: shell.taskActionNotice !== "" ? shell.taskActionNotice : shell.launchNotice
                    color: shell.actionSuccess
                    font.pixelSize: 10
                    elide: Text.ElideRight
                    Accessible.name: text
                }

                Rectangle {
                    visible: shell.loading
                    Layout.fillWidth: true
                    Layout.preferredHeight: 92
                    radius: 10
                    color: "#171c25"

                    Column {
                        anchors.centerIn: parent
                        spacing: 8

                        BusyIndicator {
                            anchors.horizontalCenter: parent.horizontalCenter
                            width: 28
                            height: 28
                            running: shell.loading
                        }

                        Text {
                            text: "Waiting for the first snapshot…"
                            color: "#9ba5b5"
                            font.pixelSize: 11
                        }
                    }
                }

                Rectangle {
                    visible: !shell.loading && shell.projects.length === 0
                    Layout.fillWidth: true
                    Layout.preferredHeight: 92
                    radius: 10
                    color: "#171c25"
                    border.width: 1
                    border.color: "#2d3542"

                    Column {
                        anchors.centerIn: parent
                        spacing: 5

                        Text {
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: "No active projects"
                            color: "#e6e9ef"
                            font.pixelSize: 13
                            font.weight: Font.Medium
                        }

                        Text {
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: "Run facets projects list to sync Kata projects."
                            color: "#8993a4"
                            font.pixelSize: 10
                        }
                    }
                }

                ScrollView {
                    id: projectScroll
                    visible: !shell.loading && shell.projects.length > 0
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    Layout.minimumHeight: 70
                    clip: true
                    ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
                    ScrollBar.vertical.policy: ScrollBar.AsNeeded

                    Column {
                        id: projectsColumn
                        width: projectScroll.availableWidth
                        spacing: 8

                        Repeater {
                            model: shell.projects

                            delegate: Rectangle {
                                id: projectCard
                                required property var modelData
                                readonly property bool expanded: shell.isExpanded(modelData.id)
                                width: projectsColumn.width
                                implicitHeight: projectContent.implicitHeight + 2
                                radius: 10
                                color: "#171c25"
                                border.width: 1
                                border.color: expanded ? "#465579" : "#2b3340"

                                ColumnLayout {
                                    id: projectContent
                                    width: parent.width
                                    spacing: 0

                                    Button {
                                        id: disclosureButton
                                        Accessible.name: (projectCard.expanded ? "Collapse " : "Expand ") + projectCard.modelData.name
                                        Layout.fillWidth: true
                                        Layout.preferredHeight: 50
                                        rightPadding: 48
                                        flat: true
                                        onClicked: shell.toggleProject(projectCard.modelData.id)

                                        contentItem: RowLayout {
                                            spacing: 10

                                            Rectangle {
                                                Layout.preferredWidth: 24
                                                Layout.preferredHeight: 24
                                                radius: 6
                                                color: projectCard.expanded ? "#3d5284" : "#303b50"
                                                border.width: 1
                                                border.color: projectCard.expanded ? "#667fca" : "#4b5b78"

                                                Text {
                                                    anchors.centerIn: parent
                                                    text: projectCard.expanded ? "−" : "+"
                                                    color: "#d5dcff"
                                                    font.pixelSize: 15
                                                    font.weight: Font.DemiBold
                                                }
                                            }

                                            ColumnLayout {
                                                Layout.fillWidth: true
                                                spacing: 1

                                                Text {
                                                    Layout.fillWidth: true
                                                    text: projectCard.modelData.name
                                                    color: "#edf0f5"
                                                    font.pixelSize: 13
                                                    font.weight: Font.Medium
                                                    elide: Text.ElideRight
                                                }

                                                Text {
                                                    Layout.fillWidth: true
                                                    text: projectCard.modelData.directory === ""
                                                        ? "Working directory not configured"
                                                        : projectCard.modelData.tasks.length + (projectCard.modelData.tasks.length === 1 ? " open task" : " open tasks")
                                                    color: projectCard.modelData.directory === "" ? "#d4a85d" : "#7f8999"
                                                    font.pixelSize: 10
                                                    elide: Text.ElideMiddle
                                                }
                                            }

                                            Rectangle {
                                                Layout.preferredWidth: 27
                                                Layout.preferredHeight: 22
                                                radius: 11
                                                color: "#252e3c"

                                                Text {
                                                    anchors.centerIn: parent
                                                    text: projectCard.modelData.tasks.length
                                                    color: "#abb5c5"
                                                    font.pixelSize: 10
                                                    font.weight: Font.Medium
                                                }
                                            }
                                        }

                                        background: Rectangle {
                                            radius: 9
                                            color: disclosureButton.hovered ? "#202733" : "transparent"
                                        }
                                    }

                                    ColumnLayout {
                                        visible: projectCard.expanded
                                        Layout.fillWidth: true
                                        Layout.leftMargin: 16
                                        Layout.rightMargin: 12
                                        Layout.bottomMargin: 10
                                        spacing: 5

                                        Rectangle {
                                            visible: projectCard.modelData.tasks.length === 0
                                            Layout.fillWidth: true
                                            Layout.preferredHeight: 34
                                            color: "transparent"

                                            Text {
                                                anchors.verticalCenter: parent.verticalCenter
                                                text: "No open tasks"
                                                color: "#778292"
                                                font.pixelSize: 11
                                            }
                                        }

                                        Rectangle {
                                            visible: projectCard.modelData.tasks.length > 0
                                            Layout.fillWidth: true
                                            Layout.preferredHeight: 1
                                            Layout.bottomMargin: 2
                                            color: "#293242"
                                        }

                                        Repeater {
                                            model: projectCard.modelData.tasks

                                            delegate: ColumnLayout {
                                                id: taskRow
                                                required property var modelData
                                                required property int index
                                                Layout.fillWidth: true
                                                Layout.minimumHeight: 56
                                                spacing: shell.space1

                                                RowLayout {
                                                    Layout.fillWidth: true
                                                    spacing: shell.space2

                                                    Text {
                                                        Layout.preferredWidth: 13
                                                        text: taskRow.index === projectCard.modelData.tasks.length - 1 ? "└" : "├"
                                                        color: "#59667a"
                                                        font.pixelSize: 12
                                                    }

                                                    Text {
                                                        Layout.fillWidth: true
                                                        text: taskRow.modelData.title
                                                        color: "#d9dee7"
                                                        font.pixelSize: 11
                                                        wrapMode: Text.Wrap
                                                    }

                                                    Text {
                                                        Layout.preferredWidth: 44
                                                        horizontalAlignment: Text.AlignRight
                                                        text: taskRow.modelData.id
                                                        color: "#697487"
                                                        font.family: "monospace"
                                                        font.pixelSize: 9
                                                        elide: Text.ElideLeft
                                                    }
                                                }

                                                RowLayout {
                                                    Layout.fillWidth: true
                                                    Layout.leftMargin: shell.space3
                                                    spacing: shell.space2

                                                    Text {
                                                        Layout.fillWidth: true
                                                        text: (taskRow.modelData.priority !== null ? "P" + taskRow.modelData.priority : "")
                                                            + (taskRow.modelData.priority !== null && taskRow.modelData.assignee !== "" ? " · " : "")
                                                            + taskRow.modelData.assignee
                                                        color: "#747f90"
                                                        font.pixelSize: 9
                                                        elide: Text.ElideRight
                                                    }

                                                    CheckBox {
                                                        id: topTaskCheck
                                                        Layout.preferredWidth: 42
                                                        Layout.preferredHeight: 28
                                                        enabled: !shell.taskActionBusy && !taskActionDialog.opened
                                                        checked: taskRow.modelData.top
                                                        text: "Top"
                                                        Accessible.name: (checked ? "Remove " : "Mark ") + "top-task status for " + taskRow.modelData.id
                                                        Accessible.description: "Persist whether this task appears in today's focus list"
                                                        onClicked: shell.toggleTopTask(projectCard.modelData.id, taskRow.modelData)

                                                        ToolTip.visible: hovered
                                                        ToolTip.delay: 400
                                                        ToolTip.text: checked ? "Remove from top tasks" : "Mark as top task"

                                                        indicator: Rectangle {
                                                            x: 0
                                                            y: (topTaskCheck.height - height) / 2
                                                            width: 16
                                                            height: 16
                                                            radius: 4
                                                            color: topTaskCheck.checked ? shell.actionAccent : shell.actionSurface
                                                            border.width: 1
                                                            border.color: topTaskCheck.checked
                                                                ? shell.actionAccent
                                                                : shell.actionBorder

                                                            Text {
                                                                anchors.centerIn: parent
                                                                text: topTaskCheck.checked ? "✓" : ""
                                                                color: "#ffffff"
                                                                font.pixelSize: 11
                                                                font.weight: Font.DemiBold
                                                            }
                                                        }

                                                        contentItem: Text {
                                                            leftPadding: 22
                                                            text: topTaskCheck.text
                                                            color: topTaskCheck.enabled ? shell.actionText : shell.actionMutedText
                                                            font.pixelSize: 9
                                                            verticalAlignment: Text.AlignVCenter
                                                        }
                                                    }

                                                    Button {
                                                        id: commentButton
                                                        Layout.preferredWidth: 64
                                                        Layout.preferredHeight: 28
                                                        enabled: !shell.taskActionBusy && !taskActionDialog.opened
                                                        text: "Comment"
                                                        flat: true
                                                        Accessible.name: "Comment on task " + taskRow.modelData.id + ": " + taskRow.modelData.title
                                                        Accessible.description: "Open a text prompt for a required task comment"
                                                        onClicked: shell.openTaskAction("comment", projectCard.modelData.id, taskRow.modelData)

                                                        ToolTip.visible: hovered
                                                        ToolTip.delay: 400
                                                        ToolTip.text: "Add a comment"

                                                        contentItem: Text {
                                                            text: commentButton.text
                                                            color: commentButton.enabled ? shell.actionText : shell.actionMutedText
                                                            font.pixelSize: 10
                                                            horizontalAlignment: Text.AlignHCenter
                                                            verticalAlignment: Text.AlignVCenter
                                                        }

                                                        background: Rectangle {
                                                            radius: shell.controlRadius
                                                            color: commentButton.hovered || commentButton.activeFocus
                                                                ? shell.actionSurfaceRaised : shell.actionSurface
                                                            border.width: 1
                                                            border.color: commentButton.activeFocus ? shell.actionAccent : shell.actionBorder
                                                        }
                                                    }

                                                    Button {
                                                        id: closeTaskButton
                                                        Layout.preferredWidth: 52
                                                        Layout.preferredHeight: 28
                                                        enabled: !shell.taskActionBusy && !taskActionDialog.opened
                                                        text: "Close"
                                                        flat: true
                                                        Accessible.name: "Close task " + taskRow.modelData.id + ": " + taskRow.modelData.title
                                                        Accessible.description: "Open an explicit close confirmation with an optional comment"
                                                        onClicked: shell.openTaskAction("close", projectCard.modelData.id, taskRow.modelData)

                                                        ToolTip.visible: hovered
                                                        ToolTip.delay: 400
                                                        ToolTip.text: "Close with audit evidence"

                                                        contentItem: Text {
                                                            text: closeTaskButton.text
                                                            color: closeTaskButton.enabled ? shell.actionDanger : shell.actionMutedText
                                                            font.pixelSize: 10
                                                            font.weight: Font.Medium
                                                            horizontalAlignment: Text.AlignHCenter
                                                            verticalAlignment: Text.AlignVCenter
                                                        }

                                                        background: Rectangle {
                                                            radius: shell.controlRadius
                                                            color: closeTaskButton.hovered || closeTaskButton.activeFocus
                                                                ? shell.actionErrorSurface : shell.actionSurface
                                                            border.width: 1
                                                            border.color: closeTaskButton.activeFocus ? shell.actionDanger : shell.actionBorder
                                                        }
                                                    }
                                                }
                                            }
                                        }
                                    }
                                }

                                Button {
                                    id: openButton
                                    anchors.top: parent.top
                                    anchors.right: parent.right
                                    anchors.topMargin: 9
                                    anchors.rightMargin: 8
                                    width: 32
                                    height: 32
                                    enabled: projectCard.modelData.directory !== "" && shell.launchingProjectId === ""
                                    opacity: 1
                                    Accessible.name: "Open Kata TUI for " + projectCard.modelData.name
                                    Accessible.description: projectCard.modelData.directory === ""
                                        ? "Configure with facets projects set " + projectCard.modelData.id + " directory=<path>"
                                        : "Open a floating terminal in " + projectCard.modelData.directory
                                    text: projectCard.modelData.directory === ""
                                        ? "—"
                                        : shell.launchingProjectId === projectCard.modelData.id ? "…" : "↗"
                                    flat: true
                                    onClicked: shell.launchProject(projectCard.modelData)

                                    ToolTip.visible: hovered
                                    ToolTip.delay: 400
                                    ToolTip.text: projectCard.modelData.directory === ""
                                        ? "Configure a project directory first"
                                        : "Open Kata TUI"

                                    contentItem: Text {
                                        text: openButton.text
                                        color: projectCard.modelData.directory === ""
                                            ? "#5f6978"
                                            : openButton.hovered ? "#ffffff" : "#aebcff"
                                        font.pixelSize: 15
                                        horizontalAlignment: Text.AlignHCenter
                                        verticalAlignment: Text.AlignVCenter
                                    }

                                    background: Rectangle {
                                        radius: 7
                                        color: projectCard.modelData.directory === ""
                                            ? "#1d2430"
                                            : openButton.hovered ? "#3a4c78" : "#293552"
                                        border.width: 1
                                        border.color: projectCard.modelData.directory === "" ? "#303947" : "#52699f"
                                    }
                                }
                            }
                        }
                    }
                }
            }
            Popup {
                id: taskActionDialog
                parent: Overlay.overlay
                anchors.centerIn: parent
                width: Math.min(392, panel.width - shell.space4 * 2)
                implicitHeight: taskActionContent.implicitHeight + topPadding + bottomPadding
                padding: shell.space4
                modal: true
                focus: true
                closePolicy: shell.taskActionBusy ? Popup.NoAutoClose : Popup.CloseOnEscape

                Overlay.modal: Rectangle {
                    color: shell.actionScrim
                }

                onOpened: Qt.callLater(() => taskActionText.forceActiveFocus())
                onClosed: {
                    if (!shell.taskActionBusy) {
                        taskActionText.text = "";
                        shell.taskActionError = "";
                    }
                }

                background: Rectangle {
                    color: shell.actionSurface
                    radius: 12
                    border.width: 1
                    border.color: shell.actionBorder
                }

                contentItem: ColumnLayout {
                    id: taskActionContent
                    spacing: shell.space3
                    Accessible.role: Accessible.Dialog
                    Accessible.name: shell.taskActionKind === "comment"
                        ? "Comment on task " + shell.taskActionId
                        : "Confirm closing task " + shell.taskActionId

                    Text {
                        Layout.fillWidth: true
                        text: shell.taskActionKind === "comment"
                            ? "Comment on " + shell.taskActionId
                            : "Close " + shell.taskActionId + "?"
                        color: shell.actionText
                        font.pixelSize: 15
                        font.weight: Font.DemiBold
                        wrapMode: Text.Wrap
                    }

                    Text {
                        Layout.fillWidth: true
                        text: shell.taskActionTitle
                        color: shell.actionMutedText
                        font.pixelSize: 11
                        wrapMode: Text.Wrap
                    }

                    Text {
                        visible: shell.taskActionKind === "close"
                        Layout.fillWidth: true
                        text: "This marks the task closed with a manual-confirmation audit message and evidence."
                        color: shell.actionErrorText
                        font.pixelSize: 10
                        wrapMode: Text.Wrap
                        Accessible.name: text
                    }

                    Text {
                        Layout.fillWidth: true
                        text: shell.taskActionKind === "comment" ? "Comment (required)" : "Comment (optional)"
                        color: shell.actionText
                        font.pixelSize: 10
                        font.weight: Font.Medium
                    }

                    TextArea {
                        id: taskActionText
                        Layout.fillWidth: true
                        Layout.preferredHeight: 96
                        enabled: !shell.taskActionBusy
                        placeholderText: shell.taskActionKind === "comment"
                            ? "Write a comment…"
                            : "Add context for closing this task…"
                        wrapMode: TextEdit.Wrap
                        color: shell.actionText
                        placeholderTextColor: shell.actionMutedText
                        selectionColor: shell.actionAccent
                        selectedTextColor: shell.actionText
                        font.pixelSize: 11
                        leftPadding: shell.space3
                        rightPadding: shell.space3
                        topPadding: shell.space2
                        bottomPadding: shell.space2
                        Accessible.name: shell.taskActionKind === "comment"
                            ? "Required comment"
                            : "Optional close comment"
                        Accessible.description: "Press Control Enter to submit"

                        onTextChanged: {
                            if (!shell.taskActionBusy
                                    && shell.taskActionError === "Enter a comment before submitting."
                                    && text.trim() !== "")
                                shell.taskActionError = "";
                        }

                        Keys.onPressed: event => {
                            if ((event.modifiers & Qt.ControlModifier)
                                    && (event.key === Qt.Key_Return || event.key === Qt.Key_Enter)) {
                                shell.submitTaskAction();
                                event.accepted = true;
                            }
                        }

                        background: Rectangle {
                            color: shell.actionSurfaceRaised
                            radius: shell.controlRadius
                            border.width: 1
                            border.color: taskActionText.activeFocus ? shell.actionAccent : shell.actionBorder
                        }
                    }

                    Rectangle {
                        visible: shell.taskActionError !== ""
                        Layout.fillWidth: true
                        implicitHeight: actionErrorLabel.implicitHeight + shell.space4
                        color: shell.actionErrorSurface
                        radius: shell.controlRadius
                        border.width: 1
                        border.color: shell.actionErrorBorder

                        Text {
                            id: actionErrorLabel
                            anchors.fill: parent
                            anchors.margins: shell.space2
                            text: shell.taskActionError
                            color: shell.actionErrorText
                            font.pixelSize: 10
                            wrapMode: Text.Wrap
                            Accessible.name: "Task action error: " + text
                        }
                    }

                    RowLayout {
                        Layout.fillWidth: true
                        spacing: shell.space2

                        BusyIndicator {
                            Layout.preferredWidth: 24
                            Layout.preferredHeight: 24
                            visible: shell.taskActionBusy
                            running: shell.taskActionBusy
                            Accessible.name: "Submitting task action"
                        }

                        Item {
                            Layout.fillWidth: true
                        }

                        Button {
                            id: cancelTaskActionButton
                            Layout.preferredWidth: 72
                            Layout.preferredHeight: 32
                            enabled: !shell.taskActionBusy
                            text: "Cancel"
                            flat: true
                            Accessible.name: "Cancel task action"
                            onClicked: taskActionDialog.close()

                            contentItem: Text {
                                text: cancelTaskActionButton.text
                                color: cancelTaskActionButton.enabled ? shell.actionMutedText : shell.actionBorder
                                font.pixelSize: 11
                                horizontalAlignment: Text.AlignHCenter
                                verticalAlignment: Text.AlignVCenter
                            }

                            background: Rectangle {
                                radius: shell.controlRadius
                                color: cancelTaskActionButton.hovered || cancelTaskActionButton.activeFocus
                                    ? shell.actionSurfaceRaised : "transparent"
                                border.width: cancelTaskActionButton.activeFocus ? 1 : 0
                                border.color: shell.actionAccent
                            }
                        }

                        Button {
                            id: submitTaskActionButton
                            Layout.preferredWidth: 104
                            Layout.preferredHeight: 32
                            enabled: !shell.taskActionBusy
                            text: shell.taskActionBusy
                                ? (shell.taskActionKind === "comment" ? "Adding…" : "Closing…")
                                : (shell.taskActionKind === "comment" ? "Add comment" : "Close task")
                            flat: true
                            Accessible.name: text
                            Accessible.description: shell.taskActionKind === "comment"
                                ? "Submit the required comment"
                                : "Confirm close with audit evidence"
                            onClicked: shell.submitTaskAction()

                            contentItem: Text {
                                text: submitTaskActionButton.text
                                color: shell.actionText
                                font.pixelSize: 11
                                font.weight: Font.Medium
                                horizontalAlignment: Text.AlignHCenter
                                verticalAlignment: Text.AlignVCenter
                            }

                            background: Rectangle {
                                radius: shell.controlRadius
                                color: shell.taskActionKind === "close"
                                    ? (submitTaskActionButton.hovered ? shell.actionDanger : shell.actionErrorBorder)
                                    : (submitTaskActionButton.hovered ? shell.actionAccent : shell.actionAccentMuted)
                                border.width: submitTaskActionButton.activeFocus ? 2 : 0
                                border.color: shell.actionText
                                opacity: submitTaskActionButton.enabled ? 1 : 0.55
                            }
                        }
                    }
                }
            }
        }
    }
}
