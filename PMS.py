# PMS.py

import sys
import subprocess
import threading
import time
import os
from dataclasses import dataclass
from typing import List

try:
    from PyQt5.QtWidgets import (
        QApplication, QWidget, QVBoxLayout, QHBoxLayout, QPushButton,
        QLineEdit, QLabel, QTableWidget, QTableWidgetItem, QMessageBox,
        QHeaderView, QSpinBox, QDialog, QFormLayout, QAbstractItemView, QComboBox
    )
    from PyQt5.QtCore import Qt, QTimer
    from PyQt5.QtGui import QIcon
except ModuleNotFoundError:
    print("PyQt5 is not installed. Please install it using:")
    print("    pip install PyQt5")
    sys.exit(1)


def resource_path(relative_path):
    if hasattr(sys, "_MEIPASS"):
        return os.path.join(sys._MEIPASS, relative_path)
    return os.path.join(os.path.abspath("."), relative_path)


@dataclass
class Device:
    ip: str
    name: str
    success: int = 0
    fail: int = 0
    total: int = 0
    last_result: bool = False
    is_active: bool = True


class SettingsDialog(QDialog):
    def __init__(self, parent=None):
        super().__init__(parent)
        self.setWindowTitle("Settings")
        layout = QFormLayout(self)

        self.ping_interval_spin = QSpinBox()
        self.ping_interval_spin.setMinimum(1)
        self.ping_interval_spin.setValue(parent.ping_interval)
        layout.addRow("Ping Interval (s):", self.ping_interval_spin)

        self.timeout_spin = QSpinBox()
        self.timeout_spin.setMinimum(100)
        self.timeout_spin.setMaximum(10000)
        self.timeout_spin.setSingleStep(100)
        self.timeout_spin.setValue(parent.ping_timeout)
        layout.addRow("Ping Timeout (ms):", self.timeout_spin)

        # Network interface input (QLineEdit)
        self.interface_input = QLineEdit()
        self.interface_input.setText(parent.interface_name)
        layout.addRow("Network Interface:", self.interface_input)

        btn = QPushButton("Save")
        btn.clicked.connect(self.accept)
        layout.addWidget(btn)

    def get_interval(self):
        return self.ping_interval_spin.value()

    def get_timeout(self):
        return self.timeout_spin.value()

    def get_interface(self):
        return self.interface_input.text().strip()


class PingMonitor(QWidget):
    def __init__(self):
        super().__init__()
        self.setWindowTitle("PMS")
        self.devices: List[Device] = []
        self.ping_interval = 1  # seconds
        self.ping_timeout = 1000  # ms
        self.interface_name = "enp3s0"  # default interface
        self.running = False
        self.timer = QTimer()
        self.timer.timeout.connect(self.ping_all)
        self.unnamed_count = 0

        self.toggle_btn = None

        self.init_ui()

    def init_ui(self):
        layout = QVBoxLayout(self)

        self.ip_input = QLineEdit()
        self.ip_input.setPlaceholderText("IP address")
        self.name_input = QLineEdit()
        self.name_input.setPlaceholderText("Device Name (optional)")

        add_btn = QPushButton("Add")
        add_btn.clicked.connect(lambda: self.add_device(self.ip_input.text(), self.name_input.text()))

        self.ip_input.returnPressed.connect(lambda: self.add_device(self.ip_input.text(), self.name_input.text()))
        self.name_input.returnPressed.connect(lambda: self.add_device(self.ip_input.text(), self.name_input.text()))

        top_layout = QHBoxLayout()
        top_layout.addWidget(self.ip_input)
        top_layout.addWidget(self.name_input)
        top_layout.addWidget(add_btn)
        layout.addLayout(top_layout)

        self.table = QTableWidget(0, 6)
        self.table.setHorizontalHeaderLabels(["Name", "IP", "Success", "Fail", "Total", ""])
        self.table.horizontalHeader().setSectionResizeMode(QHeaderView.Stretch)
        self.table.setEditTriggers(QAbstractItemView.NoEditTriggers)
        self.table.verticalHeader().setDefaultSectionSize(30)
        self.table.verticalHeader().setFixedWidth(50)
        self.table.setSortingEnabled(False)

        layout.addWidget(self.table)

        btn_layout = QHBoxLayout()
        self.toggle_btn = QPushButton("Start")
        self.toggle_btn.setStyleSheet("background-color: green; color: white;")
        self.toggle_btn.clicked.connect(self.toggle_start_stop)

        clear_btn = QPushButton("Clear")
        settings_btn = QPushButton("Settings")
        clear_btn.clicked.connect(self.clear_stats)
        settings_btn.clicked.connect(self.open_settings)

        btn_layout.addWidget(self.toggle_btn)
        btn_layout.addWidget(clear_btn)
        btn_layout.addWidget(settings_btn)
        layout.addLayout(btn_layout)

    def toggle_start_stop(self):
        if self.running:
            self.stop()
        else:
            self.start()

    def start(self):
        self.running = True
        self.timer.start(self.ping_interval * 1000)
        self.toggle_btn.setText("Stop")
        self.toggle_btn.setStyleSheet("background-color: red; color: white;")

    def stop(self):
        self.running = False
        self.timer.stop()
        self.refresh_table()
        self.toggle_btn.setText("Start")
        self.toggle_btn.setStyleSheet("background-color: green; color: white;")

    def add_device(self, ip: str, name: str):
        if not ip:
            QMessageBox.warning(self, "Missing Info", "Please enter an IP address.")
            return
        if not name.strip():
            self.unnamed_count += 1
            name = f"Switch{self.unnamed_count}"
        self.devices.append(Device(ip=ip, name=name))
        self.ip_input.clear()
        self.name_input.clear()
        self.refresh_table()

    def remove_device(self, index: int):
        if 0 <= index < len(self.devices):
            del self.devices[index]
            self.refresh_table()

    def refresh_table(self):
        scroll_pos = self.table.verticalScrollBar().value()
        self.table.setRowCount(len(self.devices))
        for idx, device in enumerate(self.devices):
            self.table.setItem(idx, 0, QTableWidgetItem(device.name))
            self.table.setItem(idx, 1, QTableWidgetItem(device.ip))
            self.table.setItem(idx, 2, QTableWidgetItem(str(device.success)))
            self.table.setItem(idx, 3, QTableWidgetItem(str(device.fail)))
            self.table.setItem(idx, 4, QTableWidgetItem(str(device.total)))

            old_widget = self.table.cellWidget(idx, 5)
            if old_widget is None:
                remove_btn = QPushButton()
                remove_btn.setIcon(QIcon(resource_path("icons/trash.ico")))
                remove_btn.setToolTip("Remove this device")
                remove_btn.setStyleSheet("border: none; background: none; padding: 2px;")
                remove_btn.clicked.connect(lambda _, i=idx: self.remove_device(i))
                self.table.setCellWidget(idx, 5, remove_btn)
            else:
                old_widget.clicked.disconnect()
                old_widget.clicked.connect(lambda _, i=idx: self.remove_device(i))

            self.color_row(idx, device)

        self.table.verticalScrollBar().setValue(scroll_pos)

    def color_row(self, row: int, device: Device):
        color = Qt.white
        if self.running:
            color = Qt.green if device.last_result else Qt.red
        for col in range(self.table.columnCount() - 1):
            item = self.table.item(row, col)
            if item:
                item.setBackground(color)

    def clear_stats(self):
        for device in self.devices:
            device.success = device.fail = device.total = 0
        self.refresh_table()

    def open_settings(self):
        dialog = SettingsDialog(self)
        if dialog.exec_():
            self.ping_interval = dialog.get_interval()
            self.ping_timeout = dialog.get_timeout()
            iface = dialog.get_interface()
            if iface:
                self.interface_name = iface
            if self.running:
                self.timer.start(self.ping_interval * 1000)

    def ping_all(self):
        threads = []
        for index, device in enumerate(self.devices):
            t = threading.Thread(target=self.ping_device, args=(device, index))
            t.start()
            threads.append(t)
        for t in threads:
            t.join()
        self.refresh_table()

    def ping_device(self, device: Device, index: int):
        try:
            timeout_sec = max(1, int(self.ping_timeout / 1000))
            result = subprocess.run(
                ["ping", "-I", self.interface_name, "-c", "1", "-W", str(timeout_sec), device.ip],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=timeout_sec + 1
            )
            device.total += 1
            if result.returncode == 0:
                device.success += 1
                device.last_result = True
            else:
                device.fail += 1
                device.last_result = False
        except (subprocess.TimeoutExpired, Exception):
            device.fail += 1
            device.total += 1
            device.last_result = False


def main():
    app = QApplication(sys.argv)
    win = PingMonitor()
    win.resize(1200, 700)
    win.show()
    sys.exit(app.exec_())


if __name__ == "__main__":
    main()
