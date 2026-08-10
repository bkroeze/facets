import Quickshell
import Quickshell.Io
import Quickshell.Wayland
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

ShellRoot {
    id: shell

    property bool panelVisible: false
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

    function isTask(value) {
        return value !== null
            && typeof value === "object"
            && typeof value.id === "string"
            && typeof value.title === "string"
            && typeof value.status === "string"
            && (value.priority === null || typeof value.priority === "number")
            && typeof value.assignee === "string"
            && typeof value.updated_at === "string";
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

            Keys.onEscapePressed: shell.panelVisible = false

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
                    visible: shell.launchError !== "" || shell.refreshError !== "" || shell.parseError !== "" || shell.disconnected
                    Layout.fillWidth: true
                    implicitHeight: statusColumn.implicitHeight + 18
                    radius: 9
                    color: shell.launchError !== "" || shell.parseError !== "" ? "#3a2429" : "#302b20"
                    border.width: 1
                    border.color: shell.launchError !== "" || shell.parseError !== "" ? "#70404a" : "#66583a"

                    ColumnLayout {
                        id: statusColumn
                        anchors.fill: parent
                        anchors.margins: 9
                        spacing: 3

                        Text {
                            Layout.fillWidth: true
                            text: shell.launchError !== ""
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
                    visible: shell.launchNotice !== ""
                    Layout.fillWidth: true
                    text: shell.launchNotice
                    color: "#8fc9a3"
                    font.pixelSize: 10
                    elide: Text.ElideRight
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

                                            delegate: RowLayout {
                                                id: taskRow
                                                required property var modelData
                                                required property int index
                                                Layout.fillWidth: true
                                                Layout.minimumHeight: 30
                                                spacing: 8

                                                Text {
                                                    Layout.preferredWidth: 13
                                                    text: taskRow.index === projectCard.modelData.tasks.length - 1 ? "└" : "├"
                                                    color: "#59667a"
                                                    font.pixelSize: 12
                                                }

                                                ColumnLayout {
                                                    Layout.fillWidth: true
                                                    spacing: 0

                                                    Text {
                                                        Layout.fillWidth: true
                                                        text: taskRow.modelData.title
                                                        color: "#d9dee7"
                                                        font.pixelSize: 11
                                                        wrapMode: Text.Wrap
                                                    }

                                                    Text {
                                                        visible: taskRow.modelData.assignee !== "" || taskRow.modelData.priority !== null
                                                        Layout.fillWidth: true
                                                        text: (taskRow.modelData.priority !== null ? "P" + taskRow.modelData.priority : "")
                                                            + (taskRow.modelData.priority !== null && taskRow.modelData.assignee !== "" ? " · " : "")
                                                            + taskRow.modelData.assignee
                                                        color: "#747f90"
                                                        font.pixelSize: 9
                                                    }
                                                }

                                                Text {
                                                    Layout.preferredWidth: 38
                                                    horizontalAlignment: Text.AlignRight
                                                    text: taskRow.modelData.id
                                                    color: "#697487"
                                                    font.family: "monospace"
                                                    font.pixelSize: 9
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
        }
    }
}
