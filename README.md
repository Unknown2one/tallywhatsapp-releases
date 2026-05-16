# Tally Prime to WhatsApp Integration

A highly robust, asynchronous, and non-blocking integration for sending Vouchers (Sales, Receipts, etc.) from Tally Prime directly to WhatsApp.

## 🚀 Overview

This repository provides a complete End-to-End solution to seamlessly transfer voucher data from Tally Prime to WhatsApp. It avoids legacy Tally freezing problems by using an asynchronous architecture consisting of:
1. **Tally TDL (`Tally-TDL`)**: Custom TDL to extract voucher data (Party name, phone number, PDF, etc.) from Tally and trigger the export.
2. **C# COM Interface (`Tally-COM-Interface`)**: Acts as a lightweight middleware DLL. Tally invokes this DLL, and the DLL instantly dispatches the request to our local Go server, without blocking the Tally UI.
3. **Go Backend Bridge (`Backend-Bridge`)**: A lightning-fast, concurrent Go server that queues incoming requests and handles the actual WhatsApp API delivery asynchronously.

---

## 📁 Repository Structure

* **`Tally-TDL/`**: Contains the TDL files (e.g., `MinimalTest.tdl`). This gets mapped into Tally Prime.
* **`Tally-COM-Interface/`**: C# .NET Library project. Must be compiled into a `.dll` and registered via `regasm`.
* **`Backend-Bridge/`**: Go Server application. Must be running in the background to dispatch messages.
* **`Build-Scripts/`**: Contains helper scripts (like `CompileAndRegister.bat`) to rebuild and register the C# library easily.

---

## 🛠️ Installation & Setup

### Step 1: Run the Go Backend Bridge
1. Install [Go](https://go.dev/doc/install) if you haven't already.
2. Open a terminal and navigate to the `Backend-Bridge` folder:
   ```cmd
   cd Backend-Bridge
   ```
3. Run the Go server:
   ```cmd
   go run main.go
   ```
   **Important:** *Keep this terminal window open in the background! This server processes the messages asynchronously.*

### Step 2: Compile and Register the C# COM DLL
1. Open a Command Prompt as **Administrator**.
2. Navigate to your project directory, then into the `Build-Scripts` directory.
   ```cmd
   cd Build-Scripts
   ```
3. Run the compilation script to compile the DLL and register it in the Windows Registry (this allows Tally to communicate with it):
   ```cmd
   CompileAndRegister.bat
   ```
   *(Note: This requires the .NET Framework 4.0 or above)*

### Step 3: Load TDL in Tally Prime
1. Open Tally Prime.
2. Press **F1 (Help)** -> **TDL & Add-On** -> **F4 (Manage Local TDLs)**.
3. Set **Load selected TDL files on startup** to `Yes`.
4. Add the full absolute path to the TDL file located in the `Tally-TDL` folder (e.g., `D:\whatstallysender\Tally-TDL\MinimalTest.tdl`).
5. Save the configuration and restart Tally to ensure the UI changes take effect.

---

## 💡 Usage

1. **Open a Voucher:** Open any Sales or Receipt voucher in Tally Prime.
2. **Click WhatsApp:** Click the custom "WhatsApp" button on the top menu (added by the TDL).
3. **Non-Blocking Execution:** The COM Interface receives the command and passes it to the Go server immediately. Tally remains perfectly responsive and you can continue working!
4. **Delivery:** The Go server queues the message, processes the PDF, and reliably completes the API call to WhatsApp in the background.

---

## 🤝 Contributing

We welcome pull requests! Feel free to fork this project, submit improvements, and report issues. Let's make Tally to WhatsApp integrations fast, un-frozen, and better for everyone!
# TallyWhatsapp-Sender
