import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui

BarWidget {
    id: root

    moduleName: "facets"

    readonly property string home: Quickshell.env("HOME") || ""
    readonly property string dataHome: Quickshell.env("XDG_DATA_HOME") || home + "/.local/share"
    readonly property url iconSource: "file://" + dataHome + "/facets/facets.svg"
    property string focusText: ""
    readonly property string displayTitle: truncateFocus(focusText)

    function truncateFocus(value) {
        const text = String(value || "").trim()
        if (text.length <= 40)
            return text
        return text.substring(0, 39).replace(/\s+$/, "") + "…"
    }

    function refreshFocus() {
        if (!focusProcess.running)
            focusProcess.running = true
    }

    function applyFocusOutput(raw) {
        let document
        try {
            document = JSON.parse(String(raw || "{}"))
        } catch (error) {
            return
        }

        if (document.focus && typeof document.focus.text === "string")
            focusText = document.focus.text.trim()
        else
            focusText = ""
    }

    implicitWidth: content.implicitWidth + 16
    implicitHeight: bar ? bar.barSize : 26

    Process {
        id: focusProcess
        command: ["facets", "--format", "json", "focus", "show"]

        stdout: StdioCollector {
            waitForEnd: true
            onStreamFinished: root.applyFocusOutput(text)
        }
    }

    Timer {
        interval: 30000
        repeat: true
        running: true
        onTriggered: root.refreshFocus()
    }

    Component.onCompleted: root.refreshFocus()

    Row {
        id: content
        anchors.centerIn: parent
        spacing: displayTitle !== "" ? 6 : 0

        Image {
            width: 16
            height: 16
            source: root.iconSource
            fillMode: Image.PreserveAspectFit
            smooth: true
        }

        Text {
            visible: root.displayTitle !== ""
            text: root.displayTitle
            color: root.bar ? root.bar.foreground : "white"
            font.family: root.bar ? root.bar.fontFamily : "sans-serif"
            font.pixelSize: 12
            verticalAlignment: Text.AlignVCenter
        }
    }

    MouseArea {
        anchors.fill: parent
        acceptedButtons: Qt.LeftButton
        hoverEnabled: true
        cursorShape: Qt.PointingHandCursor

        onEntered: {
            if (root.bar)
                root.bar.showTooltip(root, root.displayTitle === "" ? "Facets" : root.displayTitle)
        }
        onExited: {
            if (root.bar)
                root.bar.hideTooltip(root)
        }
        onClicked: {
            if (root.bar)
                root.bar.run("omarchy-shell shell toggle facets")
        }
    }
}
